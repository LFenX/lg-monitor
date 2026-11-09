package transport

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const teamInvitePrefix = "lgmon-team-v1:"
const TeamResponseLimit = 16 * 1024 * 1024

type TeamInvitation struct {
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
	Code        string `json:"code"`
}

func ValidateTeamAddress(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" || len(value) > 2048 || strings.ContainsAny(value, "\r\n\t ") {
		return fmt.Errorf("团队地址须为无凭据、路径、查询参数或片段的 HTTPS 基础地址")
	}
	if u.Port() != "" {
		if _, err := net.LookupPort("tcp", u.Port()); err != nil {
			return fmt.Errorf("团队地址端口无效：%w", err)
		}
	}
	return nil
}

func NewTeamID(prefix string, random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	var value [16]byte
	if _, err := io.ReadFull(random, value[:]); err != nil {
		return "", fmt.Errorf("生成团队身份失败：%w", err)
	}
	return prefix + "-" + hex.EncodeToString(value[:]), nil
}

func NewTeamInvitation(address, fingerprint, team string, random io.Reader) (string, string, error) {
	if err := ValidateTeamAddress(address); err != nil {
		return "", "", err
	}
	if !regexp.MustCompile(`^team-[a-z0-9-]{1,120}$`).MatchString(team) || len(fingerprint) != 64 {
		return "", "", fmt.Errorf("团队身份或 CA 指纹无效")
	}
	if random == nil {
		random = rand.Reader
	}
	code, err := newCode(random)
	if err != nil {
		return "", "", err
	}
	value := TeamInvitation{Address: strings.TrimSuffix(address, "/"), Fingerprint: fingerprint[:16], Code: team + "." + code}
	data, err := json.Marshal(value)
	if err != nil {
		return "", "", fmt.Errorf("编码团队邀请失败：%w", err)
	}
	return teamInvitePrefix + base64.RawURLEncoding.EncodeToString(data), codeHash(value.Code), nil
}

func ParseTeamInvitation(text string) (TeamInvitation, error) {
	var value TeamInvitation
	text = strings.TrimSpace(text)
	if len(text) > 16*1024 || !strings.HasPrefix(text, teamInvitePrefix) {
		return value, fmt.Errorf("团队邀请格式无效，请粘贴负责人生成的完整邀请文字")
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(text, teamInvitePrefix))
	if err != nil {
		return value, fmt.Errorf("解码团队邀请失败：%w", err)
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&value); err != nil {
		return value, fmt.Errorf("团队邀请字段无效：%w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return value, fmt.Errorf("团队邀请包含多余内容")
	}
	if err := ValidateTeamAddress(value.Address); err != nil {
		return value, err
	}
	if !regexp.MustCompile(`^[A-Fa-f0-9]{16}$`).MatchString(value.Fingerprint) || !regexp.MustCompile(`^team-[a-z0-9-]{1,120}\.[A-HJ-NP-Z2-9-]{12,18}$`).MatchString(value.Code) {
		return value, fmt.Errorf("团队邀请的指纹或邀请码格式无效")
	}
	return value, nil
}

func (i TeamInvitation) TeamID() string { return strings.SplitN(i.Code, ".", 2)[0] }
func TeamInviteHash(code string) string { return codeHash(code) }

type TeamAuthorityResponse struct {
	CAPEM []byte `json:"ca"`
}
type TeamJoinRequest struct {
	Code   string `json:"code"`
	CSRDER []byte `json:"csr"`
}
type TeamJoinResponse struct {
	TeamID         string `json:"teamId"`
	TeamName       string `json:"teamName"`
	OwnerName      string `json:"ownerName"`
	MemberID       string `json:"memberId"`
	CAPEM          []byte `json:"ca"`
	CertificatePEM []byte `json:"certificate"`
}

type TeamConnector interface {
	Exchange(context.Context, string, string, *tls.Config, any, any) error
}
type NativeTeamConnector struct{}
type TeamRemoteError struct {
	Status        int
	Code, Message string
}

func (e *TeamRemoteError) Error() string { return e.Message }

func (NativeTeamConnector) Exchange(ctx context.Context, address, path string, configuration *tls.Config, input, output any) error {
	if err := ValidateTeamAddress(address); err != nil {
		return err
	}
	if !strings.HasPrefix(path, "/team/v1/") || strings.ContainsAny(path, "\r\n") {
		return fmt.Errorf("团队请求路径无效")
	}
	var body io.Reader
	method := http.MethodGet
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("编码团队请求失败：%w", err)
		}
		body = bytes.NewReader(data)
		method = http.MethodPost
	}
	r, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(address, "/")+path, body)
	if err != nil {
		return fmt.Errorf("创建团队请求失败：%w", err)
	}
	if input != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	connection := &http.Transport{TLSClientConfig: configuration, Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 10 * time.Second, TLSHandshakeTimeout: 10 * time.Second}
	defer connection.CloseIdleConnections()
	client := &http.Client{Transport: connection, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(r)
	if err != nil {
		return fmt.Errorf("负责人的电脑不在线或安全连接失败：%w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, TeamResponseLimit+1))
	if err != nil {
		return fmt.Errorf("读取团队响应失败：%w", err)
	}
	if len(data) > TeamResponseLimit {
		return fmt.Errorf("团队响应超过 16 MiB 上限")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var fault struct {
			Error struct{ Code, Message string }
		}
		if json.Unmarshal(data, &fault) != nil || len(fault.Error.Code) > 80 || len(fault.Error.Message) > 512 || fault.Error.Message == "" {
			return &TeamRemoteError{Status: response.StatusCode, Code: "team_unavailable", Message: "团队请求未成功，请刷新或联系负责人"}
		}
		return &TeamRemoteError{Status: response.StatusCode, Code: fault.Error.Code, Message: fault.Error.Message}
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("团队响应格式无效：%w", err)
	}
	return nil
}

func PreviewTeamInvitation(ctx context.Context, invitation TeamInvitation, connector TeamConnector, now func() time.Time) ([]byte, string, error) {
	if connector == nil || now == nil {
		return nil, "", fmt.Errorf("团队配对需要连接器和时钟")
	}
	var response TeamAuthorityResponse
	// No invitation or private material is sent before checking the public CA fingerprint.
	if err := connector.Exchange(ctx, invitation.Address, "/team/v1/authority", &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, Time: now}, nil, &response); err != nil {
		return nil, "", err
	}
	ca, err := parseCertificate(response.CAPEM)
	if err != nil || !ca.IsCA || ca.CheckSignatureFrom(ca) != nil || now().Before(ca.NotBefore) || !now().Before(ca.NotAfter) {
		return nil, "", fmt.Errorf("团队 CA 无效或已过期")
	}
	digest := sha256.Sum256(ca.Raw)
	fingerprint := strings.ToUpper(hex.EncodeToString(digest[:]))
	if err := VerifyFingerprint(fingerprint, invitation.Fingerprint); err != nil {
		return nil, "", err
	}
	return response.CAPEM, fingerprint, nil
}

func (a *Authority) SignTeamCSR(der []byte) (string, []byte, error) {
	if len(der) > 16*1024 {
		return "", nil, fmt.Errorf("成员证书申请过大")
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil || !regexp.MustCompile(`^member-[a-f0-9]{32}$`).MatchString(csr.Subject.CommonName) {
		return "", nil, fmt.Errorf("成员证书身份无效")
	}
	return a.SignCSR(der)
}

func (a *Authority) TeamTLS(ctx context.Context, hosts []string, verify func(context.Context, *x509.Certificate) error) (*tls.Config, error) {
	sans, err := a.options.SANs.Discover(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取团队证书地址失败：%w", err)
	}
	for _, host := range hosts {
		if host == "" || host == "0.0.0.0" || host == "::" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			sans.IPs = appendUniqueIP(sans.IPs, ip)
		} else {
			sans.Names = append(sans.Names, host)
		}
	}
	key, ok := a.server.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("团队服务证书私钥类型无效")
	}
	certificate, err := issueCertificate(a.options, a.ca, a.key, "lgmon-team-server", &key.PublicKey, x509.ExtKeyUsageServerAuth, sans)
	if err != nil {
		return nil, err
	}
	leaf, err := parseCertificate(certificate)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(a.ca)
	base := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{{Certificate: [][]byte{leaf.Raw, a.ca.Raw}, PrivateKey: key, Leaf: leaf}}, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: roots, Time: a.options.Now}
	base.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		configuration := base.Clone()
		configuration.GetConfigForClient = nil
		configuration.VerifyConnection = func(connection tls.ConnectionState) error {
			if len(connection.PeerCertificates) == 0 {
				return nil
			}
			if verify == nil {
				return fmt.Errorf("团队成员认证未配置")
			}
			return verify(hello.Context(), connection.PeerCertificates[0])
		}
		return configuration, nil
	}
	return base, nil
}

func LoadTeamTLS(ctx context.Context, keys KeyStore, caPEM, certificatePEM []byte, keyRef, fingerprint string, now func() time.Time) (*tls.Config, error) {
	ca, err := parseCertificate(caPEM)
	if err != nil || !ca.IsCA || ca.CheckSignatureFrom(ca) != nil {
		return nil, fmt.Errorf("本机团队 CA 无效")
	}
	digest := sha256.Sum256(ca.Raw)
	if err := VerifyFingerprint(strings.ToUpper(hex.EncodeToString(digest[:])), fingerprint); err != nil {
		return nil, err
	}
	identity, err := loadIdentity(ctx, keys, certificatePEM, keyRef, ca, x509.ExtKeyUsageClientAuth, now())
	if err != nil {
		return nil, err
	}
	if !regexp.MustCompile(`^member-[a-f0-9]{32}$`).MatchString(identity.Leaf.Subject.CommonName) {
		return nil, fmt.Errorf("本机证书不是团队成员身份")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{identity}, Time: now}, nil
}

func JoinTeam(ctx context.Context, invitation TeamInvitation, expected string, connector TeamConnector, keys KeyStore, random io.Reader, now func() time.Time) (TeamJoinResponse, string, string, error) {
	var response TeamJoinResponse
	if keys == nil || now == nil {
		return response, "", "", fmt.Errorf("加入团队需要密钥库和时钟")
	}
	if random == nil {
		random = rand.Reader
	}
	caPEM, fingerprint, err := PreviewTeamInvitation(ctx, invitation, connector, now)
	if err != nil {
		return response, "", "", err
	}
	if err := VerifyFingerprint(fingerprint, expected); err != nil {
		return response, "", "", err
	}
	ca, _ := parseCertificate(caPEM)
	id, err := NewTeamID("member", random)
	if err != nil {
		return response, "", "", err
	}
	key, csr, err := NewClientRequest(id, random)
	if err != nil {
		return response, "", "", err
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if err := connector.Exchange(ctx, invitation.Address, "/team/v1/join", &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Time: now}, TeamJoinRequest{Code: invitation.Code, CSRDER: csr}, &response); err != nil {
		return response, "", "", err
	}
	leaf, err := parseCertificate(response.CertificatePEM)
	if err != nil || response.MemberID != id || response.TeamID != invitation.TeamID() || !bytes.Equal(caPEM, response.CAPEM) {
		return response, "", "", fmt.Errorf("团队返回的成员身份不匹配")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil || leaf.Subject.CommonName != id || !samePublicKey(leaf.PublicKey, &key.PublicKey) {
		return response, "", "", fmt.Errorf("团队成员证书未通过校验")
	}
	ref, err := saveKey(ctx, keys, random, key)
	if err != nil {
		return response, "", "", err
	}
	return response, ref, fingerprint, nil
}
