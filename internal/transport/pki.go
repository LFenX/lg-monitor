package transport

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"regexp"
	"strings"
	"time"
)

const adminName = "lgmon-local-admin"

type KeyStore interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte) error
}

type StateStore interface {
	Load(context.Context, string) ([]byte, error)
	Save(context.Context, string, []byte, bool) error
}

type SANs struct {
	Names []string
	IPs   []net.IP
}

type SANSource interface {
	Discover(context.Context) (SANs, error)
}

type AuthorityOptions struct {
	Keys         KeyStore
	State        StateStore
	SANs         SANSource
	Now          func() time.Time
	Random       io.Reader
	ExistingOnly bool
}

type authorityDocument struct {
	Version   int    `json:"version"`
	CA        []byte `json:"ca"`
	Server    []byte `json:"server"`
	Admin     []byte `json:"admin"`
	CAKey     string `json:"caKeyRef"`
	ServerKey string `json:"serverKeyRef"`
	AdminKey  string `json:"adminKeyRef"`
}

type Authority struct {
	options AuthorityOptions
	doc     authorityDocument
	ca      *x509.Certificate
	key     *ecdsa.PrivateKey
	server  tls.Certificate
	admin   tls.Certificate
}

func NewAuthority(ctx context.Context, options AuthorityOptions) (*Authority, error) {
	if options.Keys == nil || options.State == nil || options.SANs == nil {
		return nil, fmt.Errorf("证书管理需要密钥库、状态库和主机地址源")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	data, err := options.State.Load(ctx, "authority")
	if errors.Is(err, os.ErrNotExist) {
		if options.ExistingOnly {
			return nil, fmt.Errorf("尚无监控器证书，请先运行 lgmon run --role monitor：%w", err)
		}
		doc, createErr := createAuthority(ctx, options)
		if createErr != nil {
			return nil, createErr
		}
		data, err = json.Marshal(doc)
		if err != nil {
			return nil, fmt.Errorf("编码 CA 信息失败：%w", err)
		}
		if err := options.State.Save(ctx, "authority", data, false); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return nil, err
			}
			data, err = options.State.Load(ctx, "authority")
			if err != nil {
				return nil, err
			}
		}
	} else if err != nil {
		return nil, err
	}
	var doc authorityDocument
	if err := decodeState(data, &doc); err != nil {
		return nil, err
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("CA 状态版本无效，请恢复备份，不要删除既有 CA")
	}
	ca, err := parseCertificate(doc.CA)
	if err != nil || !ca.IsCA || ca.CheckSignatureFrom(ca) != nil || options.Now().Before(ca.NotBefore) || !options.Now().Before(ca.NotAfter) {
		return nil, fmt.Errorf("CA 证书无效或已过期，请恢复原 CA 备份")
	}
	key, err := loadKey(ctx, options.Keys, doc.CAKey)
	if err != nil {
		return nil, err
	}
	if !samePublicKey(ca.PublicKey, &key.PublicKey) {
		return nil, fmt.Errorf("CA 证书和私钥不匹配，请恢复备份")
	}
	server, err := loadIdentity(ctx, options.Keys, doc.Server, doc.ServerKey, ca, x509.ExtKeyUsageServerAuth, options.Now())
	if err != nil {
		return nil, err
	}
	admin, err := loadIdentity(ctx, options.Keys, doc.Admin, doc.AdminKey, ca, x509.ExtKeyUsageClientAuth, options.Now())
	if err != nil {
		return nil, err
	}
	if admin.Leaf.Subject.CommonName != adminName {
		return nil, fmt.Errorf("本机管理证书身份无效")
	}
	return &Authority{options: options, doc: doc, ca: ca, key: key, server: server, admin: admin}, nil
}

func createAuthority(ctx context.Context, opts AuthorityOptions) (authorityDocument, error) {
	sans, err := opts.SANs.Discover(ctx)
	if err != nil {
		return authorityDocument{}, fmt.Errorf("读取证书主机地址失败：%w", err)
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), opts.Random)
	if err != nil {
		return authorityDocument{}, fmt.Errorf("生成 CA 私钥失败：%w", err)
	}
	serial, err := newSerial(opts.Random)
	if err != nil {
		return authorityDocument{}, err
	}
	now := opts.Now()
	ca := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "lg-monitor private CA"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	caDER, err := x509.CreateCertificate(opts.Random, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return authorityDocument{}, fmt.Errorf("签发 CA 失败：%w", err)
	}
	ca, err = x509.ParseCertificate(caDER)
	if err != nil {
		return authorityDocument{}, fmt.Errorf("解析新 CA 失败：%w", err)
	}
	caRef, err := saveKey(ctx, opts.Keys, opts.Random, caKey)
	if err != nil {
		return authorityDocument{}, err
	}
	serverKey, serverPEM, err := issueKeyPair(opts, ca, caKey, "lgmon-server", x509.ExtKeyUsageServerAuth, sans)
	if err != nil {
		return authorityDocument{}, err
	}
	serverRef, err := saveKey(ctx, opts.Keys, opts.Random, serverKey)
	if err != nil {
		return authorityDocument{}, err
	}
	adminKey, adminPEM, err := issueKeyPair(opts, ca, caKey, adminName, x509.ExtKeyUsageClientAuth, SANs{})
	if err != nil {
		return authorityDocument{}, err
	}
	adminRef, err := saveKey(ctx, opts.Keys, opts.Random, adminKey)
	if err != nil {
		return authorityDocument{}, err
	}
	return authorityDocument{Version: 1, CA: pemCertificate(caDER), Server: serverPEM, Admin: adminPEM, CAKey: caRef, ServerKey: serverRef, AdminKey: adminRef}, nil
}

func newSerial(random io.Reader) (*big.Int, error) {
	serial, err := rand.Int(random, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("生成证书序号失败：%w", err)
	}
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	return serial, nil
}

func issueKeyPair(opts AuthorityOptions, ca *x509.Certificate, issuer *ecdsa.PrivateKey, name string, usage x509.ExtKeyUsage, sans SANs) (*ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), opts.Random)
	if err != nil {
		return nil, nil, fmt.Errorf("生成证书私钥失败：%w", err)
	}
	certificate, err := issueCertificate(opts, ca, issuer, name, &key.PublicKey, usage, sans)
	return key, certificate, err
}

func issueCertificate(opts AuthorityOptions, ca *x509.Certificate, issuer *ecdsa.PrivateKey, name string, public *ecdsa.PublicKey, usage x509.ExtKeyUsage, sans SANs) ([]byte, error) {
	serial, err := newSerial(opts.Random)
	if err != nil {
		return nil, err
	}
	now := opts.Now()
	if !now.Before(ca.NotAfter) {
		return nil, fmt.Errorf("CA 已过期，不能签发证书")
	}
	expires := ca.NotAfter
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, NotBefore: now.Add(-5 * time.Minute), NotAfter: expires, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, DNSNames: sans.Names, IPAddresses: sans.IPs}
	der, err := x509.CreateCertificate(opts.Random, template, ca, public, issuer)
	if err != nil {
		return nil, fmt.Errorf("签发证书失败：%w", err)
	}
	return pemCertificate(der), nil
}

func saveKey(ctx context.Context, keys KeyStore, random io.Reader, key *ecdsa.PrivateKey) (string, error) {
	nameBytes := make([]byte, 16)
	if _, err := io.ReadFull(random, nameBytes); err != nil {
		return "", fmt.Errorf("生成私钥引用失败：%w", err)
	}
	name := "transport-key-" + hex.EncodeToString(nameBytes)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", fmt.Errorf("编码私钥失败：%w", err)
	}
	defer clear(der)
	if err := keys.Set(ctx, name, der); err != nil {
		return "", fmt.Errorf("加密保存私钥失败：%w", err)
	}
	return name, nil
}

func loadKey(ctx context.Context, keys KeyStore, name string) (*ecdsa.PrivateKey, error) {
	if !regexp.MustCompile(`^transport-key-[a-f0-9]{32}$`).MatchString(name) {
		return nil, fmt.Errorf("证书私钥引用无效")
	}
	der, err := keys.Get(ctx, name)
	defer clear(der)
	if err != nil {
		return nil, fmt.Errorf("读取证书私钥失败：%w", err)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("解析证书私钥失败，请恢复密钥备份：%w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, fmt.Errorf("证书私钥必须为 ECDSA P-256")
	}
	return key, nil
}

func parseCertificate(data []byte) (*x509.Certificate, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("证书必须是单个 PEM CERTIFICATE")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析证书失败：%w", err)
	}
	return certificate, nil
}

func pemCertificate(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func samePublicKey(first, second any) bool {
	a, err := x509.MarshalPKIXPublicKey(first)
	if err != nil {
		return false
	}
	b, err := x509.MarshalPKIXPublicKey(second)
	return err == nil && bytes.Equal(a, b)
}

func loadIdentity(ctx context.Context, keys KeyStore, certificate []byte, ref string, ca *x509.Certificate, usage x509.ExtKeyUsage, now time.Time) (tls.Certificate, error) {
	leaf, err := parseCertificate(certificate)
	if err != nil {
		return tls.Certificate{}, err
	}
	key, err := loadKey(ctx, keys, ref)
	if err != nil {
		return tls.Certificate{}, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{usage}}); err != nil {
		return tls.Certificate{}, fmt.Errorf("证书未通过 CA/有效期/用途检查：%w", err)
	}
	if !samePublicKey(leaf.PublicKey, &key.PublicKey) {
		return tls.Certificate{}, fmt.Errorf("证书与私钥不匹配")
	}
	return tls.Certificate{Certificate: [][]byte{leaf.Raw, ca.Raw}, PrivateKey: key, Leaf: leaf}, nil
}

func decodeState(data []byte, value any) error {
	if len(data) > 1024*1024 {
		return fmt.Errorf("证书状态超过大小上限")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("证书状态无效，请恢复备份：%w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("证书状态包含多余内容")
	}
	return nil
}

func (a *Authority) Fingerprint() string {
	digest := sha256.Sum256(a.ca.Raw)
	return strings.ToUpper(hex.EncodeToString(digest[:]))
}
func (a *Authority) CAPEM() []byte { return bytes.Clone(a.doc.CA) }

func (a *Authority) SignCSR(der []byte) (string, []byte, error) {
	if len(der) > 16*1024 {
		return "", nil, fmt.Errorf("证书申请超过 16 KiB 上限")
	}
	request, err := x509.ParseCertificateRequest(der)
	if err != nil {
		return "", nil, fmt.Errorf("解析证书申请失败：%w", err)
	}
	if err := request.CheckSignature(); err != nil {
		return "", nil, fmt.Errorf("证书申请签名无效：%w", err)
	}
	id := request.Subject.CommonName
	public, ok := request.PublicKey.(*ecdsa.PublicKey)
	if !ok || public.Curve != elliptic.P256() || !regexp.MustCompile(`^[a-zA-Z0-9-]{1,128}$`).MatchString(id) || id == adminName {
		return "", nil, fmt.Errorf("采集器申请需要有效身份和 ECDSA P-256 公钥")
	}
	certificate, err := issueCertificate(a.options, a.ca, a.key, id, public, x509.ExtKeyUsageClientAuth, SANs{})
	return id, certificate, err
}

func (a *Authority) AdminTLS() *tls.Config {
	roots := x509.NewCertPool()
	roots.AddCert(a.ca)
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{a.admin}, Time: a.options.Now}
}
