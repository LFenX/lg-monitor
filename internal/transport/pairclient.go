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
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/encoding/gzip"
)

var ErrFingerprint = errors.New("CA 指纹不一致，已停止配对；请在监控器上重新核对")

type FingerprintRequired struct{ Fingerprint string }

func (e *FingerprintRequired) Error() string {
	return "请先核对监控器显示的 CA 指纹，再使用 --fingerprint 继续配对"
}

type RPCConnection interface {
	grpc.ClientConnInterface
	Close() error
}

type TLSConnector interface {
	Connect(context.Context, string, *tls.Config) (RPCConnection, error)
}

type NativeConnector struct{}

func (NativeConnector) Connect(_ context.Context, address string, configuration *tls.Config) (RPCConnection, error) {
	if _, _, err := net.SplitHostPort(address); err != nil {
		return nil, fmt.Errorf("监控器地址必须为主机:端口：%w", err)
	}
	connection, err := grpc.NewClient("passthrough:///"+address, grpc.WithNoProxy(), grpc.WithTransportCredentials(credentials.NewTLS(configuration)), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxMessageSize), grpc.MaxCallSendMsgSize(MaxMessageSize), grpc.UseCompressor(gzip.Name)))
	if err != nil {
		return nil, fmt.Errorf("创建 TLS 连接失败：%w", err)
	}
	return connection, nil
}

type JoinOptions struct {
	Address, CollectorID, Hostname, Code, Fingerprint string
	Keys                                              KeyStore
	State                                             StateStore
	Connector                                         TLSConnector
	Confirm                                           func(string) (bool, error)
	Now                                               func() time.Time
	Random                                            io.Reader
}

type clientDocument struct {
	Version     int    `json:"version"`
	CollectorID string `json:"collectorId"`
	Monitor     string `json:"monitor"`
	CA          []byte `json:"ca"`
	Certificate []byte `json:"certificate"`
	KeyRef      string `json:"keyRef"`
}

type ClientIdentity struct {
	ID          string
	Monitor     string
	Certificate tls.Certificate
	CA          *x509.Certificate
	Now         func() time.Time
}

func (c *ClientIdentity) TLS() *tls.Config {
	roots := x509.NewCertPool()
	roots.AddCert(c.CA)
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{c.Certificate}, Time: c.Now}
}

func NewClientRequest(id string, random io.Reader) (*ecdsa.PrivateKey, []byte, error) {
	if !regexp.MustCompile(`^[a-zA-Z0-9-]{1,128}$`).MatchString(id) || id == adminName {
		return nil, nil, fmt.Errorf("采集器身份无效")
	}
	if random == nil {
		random = rand.Reader
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), random)
	if err != nil {
		return nil, nil, fmt.Errorf("生成采集器私钥失败：%w", err)
	}
	der, err := x509.CreateCertificateRequest(random, &x509.CertificateRequest{Subject: pkix.Name{CommonName: id}}, key)
	if err != nil {
		return nil, nil, fmt.Errorf("创建证书申请失败：%w", err)
	}
	return key, der, nil
}

func Join(ctx context.Context, options JoinOptions) (string, error) {
	if options.Keys == nil || options.State == nil {
		return "", fmt.Errorf("配对需要本机密钥库和状态库")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.Connector == nil {
		options.Connector = NativeConnector{}
	}
	// The unverified connection fetches public CA bytes only; the code is sent on a second pinned connection.
	bootstrap, err := options.Connector.Connect(ctx, options.Address, &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, Time: options.Now})
	if err != nil {
		return "", err
	}
	queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	authority, err := lgmonv1.NewPairingClient(bootstrap).Authority(queryCtx, &lgmonv1.AuthorityRequest{})
	cancel()
	bootstrap.Close()
	if err != nil {
		return "", fmt.Errorf("读取监控器 CA 失败：%w", err)
	}
	ca, err := parseCertificate(authority.CaPem)
	if err != nil || !ca.IsCA || ca.CheckSignatureFrom(ca) != nil || options.Now().Before(ca.NotBefore) || !options.Now().Before(ca.NotAfter) {
		return "", fmt.Errorf("监控器返回的 CA 无效或已过期")
	}
	digest := sha256.Sum256(ca.Raw)
	fingerprint := strings.ToUpper(hex.EncodeToString(digest[:]))
	if options.Fingerprint != "" {
		expected := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(options.Fingerprint), ":", ""))
		if (len(expected) != 8 && len(expected) != 64) || !strings.HasPrefix(fingerprint, expected) {
			return fingerprint, ErrFingerprint
		}
	} else {
		if options.Confirm == nil {
			return fingerprint, &FingerprintRequired{Fingerprint: fingerprint}
		}
		confirmed, err := options.Confirm(fingerprint)
		if err != nil {
			return fingerprint, err
		}
		if !confirmed {
			return fingerprint, ErrFingerprint
		}
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	connection, err := options.Connector.Connect(ctx, options.Address, &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Time: options.Now})
	if err != nil {
		return fingerprint, err
	}
	defer connection.Close()
	key, csr, err := NewClientRequest(options.CollectorID, options.Random)
	if err != nil {
		return fingerprint, err
	}
	queryCtx, cancel = context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	response, err := lgmonv1.NewPairingClient(connection).Join(queryCtx, &lgmonv1.JoinRequest{Code: options.Code, CsrDer: csr, Hostname: options.Hostname})
	if err != nil {
		return fingerprint, fmt.Errorf("配对申请未完成：%w", err)
	}
	returnedCA, err := parseCertificate(response.CaPem)
	if err != nil || !bytes.Equal(returnedCA.Raw, ca.Raw) {
		return fingerprint, ErrFingerprint
	}
	if err := saveClient(ctx, options.State, options.Keys, options.Random, options.Now, options.CollectorID, options.Address, authority.CaPem, response.CertificatePem, key); err != nil {
		return fingerprint, err
	}
	return fingerprint, nil
}

func saveClient(ctx context.Context, state StateStore, keys KeyStore, random io.Reader, now func() time.Time, id, address string, caPEM, certificatePEM []byte, key *ecdsa.PrivateKey) error {
	ca, err := parseCertificate(caPEM)
	if err != nil {
		return err
	}
	leaf, err := parseCertificate(certificatePEM)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return fmt.Errorf("客户端证书校验失败：%w", err)
	}
	if leaf.Subject.CommonName != id || leaf.IsCA || !samePublicKey(leaf.PublicKey, &key.PublicKey) {
		return fmt.Errorf("客户端证书与申请身份或私钥不匹配")
	}
	ref, err := saveKey(ctx, keys, random, key)
	if err != nil {
		return err
	}
	data, err := json.Marshal(clientDocument{Version: 1, CollectorID: id, Monitor: address, CA: caPEM, Certificate: certificatePEM, KeyRef: ref})
	if err != nil {
		return fmt.Errorf("编码配对状态失败：%w", err)
	}
	return state.Save(ctx, "collector", data, true)
}

func LoadClient(ctx context.Context, state StateStore, keys KeyStore, now func() time.Time) (*ClientIdentity, error) {
	if now == nil {
		now = time.Now
	}
	data, err := state.Load(ctx, "collector")
	if err != nil {
		return nil, fmt.Errorf("读取配对信息失败，请先运行 lgmon pair join：%w", err)
	}
	var doc clientDocument
	if err := decodeState(data, &doc); err != nil {
		return nil, err
	}
	if doc.Version != 1 || doc.CollectorID == "" || doc.Monitor == "" {
		return nil, fmt.Errorf("配对状态无效，请恢复备份")
	}
	ca, err := parseCertificate(doc.CA)
	if err != nil {
		return nil, err
	}
	identity, err := loadIdentity(ctx, keys, doc.Certificate, doc.KeyRef, ca, x509.ExtKeyUsageClientAuth, now())
	if err != nil {
		return nil, err
	}
	if identity.Leaf.Subject.CommonName != doc.CollectorID || identity.Leaf.IsCA {
		return nil, fmt.Errorf("配对证书的采集器身份不符")
	}
	return &ClientIdentity{ID: doc.CollectorID, Monitor: doc.Monitor, Certificate: identity, CA: ca, Now: now}, nil
}

func SelfPair(ctx context.Context, authority *Authority, repository PairRepository, state StateStore, id, hostname, address string) (*ClientIdentity, error) {
	if _, err := state.Load(ctx, "collector"); err == nil {
		identity, err := LoadClient(ctx, state, authority.options.Keys, authority.options.Now)
		if err != nil {
			return nil, err
		}
		if identity.ID != id || !bytes.Equal(identity.CA.Raw, authority.ca.Raw) {
			return nil, fmt.Errorf("本机采集器与监控器证书不属于同一身份")
		}
		item, found, err := repository.Find(ctx, id)
		if err != nil {
			return nil, err
		}
		if !found || item.Revoked || item.Serial != identity.Certificate.Leaf.SerialNumber.String() {
			return nil, fmt.Errorf("本机采集器证书未登记或已吊销，请明确重新配对")
		}
		identity.Monitor = address
		return identity, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		// Only absence permits a new identity; malformed state is never silently replaced.
		return nil, err
	}
	key, csr, err := NewClientRequest(id, authority.options.Random)
	if err != nil {
		return nil, err
	}
	_, certificate, err := authority.SignCSR(csr)
	if err != nil {
		return nil, err
	}
	leaf, err := parseCertificate(certificate)
	if err != nil {
		return nil, err
	}
	code, err := newCode(authority.options.Random)
	if err != nil {
		return nil, err
	}
	at := authority.options.Now()
	if err := repository.AddCode(ctx, codeHash(code), at.Add(time.Minute).UnixMilli()); err != nil {
		return nil, err
	}
	if err := repository.Consume(ctx, codeHash(code), at.UnixMilli(), Credential{ID: id, Hostname: hostname, Serial: leaf.SerialNumber.String(), IssuedAt: at.UnixMilli()}); err != nil {
		return nil, err
	}
	if err := saveClient(ctx, state, authority.options.Keys, authority.options.Random, authority.options.Now, id, address, authority.CAPEM(), certificate, key); err != nil {
		return nil, err
	}
	return LoadClient(ctx, state, authority.options.Keys, authority.options.Now)
}
