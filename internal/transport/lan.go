package transport

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"
)

// LANDiscoveryPort is where a visible monitor answers "who is here" on the local network.
const LANDiscoveryPort = 7446

const lanMagic = "lgmon-lan-v1"

// LANBeacon is both the question and the answer of discovery; it carries no board data, which
// is fetched over TLS from the directory so a forged packet can only point somewhere, not lie.
type LANBeacon struct {
	Magic       string `json:"lgmon"`
	Kind        string `json:"kind"`
	Name        string `json:"name,omitempty"`
	Port        int    `json:"port,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type LANPeer struct {
	Name        string `json:"name"`
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
}

type LANBoardCard struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Services    int    `json:"services"`
	Children    int    `json:"children"`
}

type LANDirectory struct {
	Name string `json:"name"`
	// Avatar is a bundled preset key or a small data-URL picture; older versions leave it out.
	Avatar string `json:"avatar,omitempty"`
	// Hostname stays for older viewers; the owner no longer fills it, since the nickname says who it is.
	Hostname    string         `json:"hostname,omitempty"`
	Fingerprint string         `json:"fingerprint"`
	Boards      []LANBoardCard `json:"boards"`
}

type LANRequestInput struct {
	BoardID    string `json:"boardId"`
	DeviceName string `json:"deviceName"`
	Avatar     string `json:"avatar,omitempty"`
	Hostname   string `json:"hostname"`
	CSRDER     []byte `json:"csr,omitempty"`
	Secret     string `json:"secret"`
}

type LANRequestReply struct {
	ID          string `json:"id"`
	Code        string `json:"code"`
	Status      string `json:"status"`
	OwnerName   string `json:"ownerName"`
	OwnerAvatar string `json:"ownerAvatar,omitempty"`
	BoardName   string `json:"boardName"`
}

type LANStatusInput struct {
	Secret string `json:"secret"`
}

type LANStatusReply struct {
	Status         string `json:"status"`
	MemberID       string `json:"memberId,omitempty"`
	TeamID         string `json:"teamId,omitempty"`
	TeamName       string `json:"teamName,omitempty"`
	OwnerName      string `json:"ownerName,omitempty"`
	OwnerAvatar    string `json:"ownerAvatar,omitempty"`
	CAPEM          []byte `json:"ca,omitempty"`
	CertificatePEM []byte `json:"certificate,omitempty"`
}

// LANOwner rides along with each shared board, so a nickname or avatar changed after approval
// reaches the viewer on its next refresh.
type LANOwner struct {
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"`
}

// LANCode is the six-digit pairing code both screens show. It binds the owner's CA, the
// requester's public key and the request, so a machine in the middle cannot keep both equal.
func LANCode(fingerprint string, publicKey []byte, requestID string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(fingerprint) + "\n" + requestID + "\n" + hex.EncodeToString(publicKey)))
	value := binary.BigEndian.Uint32(sum[:4]) % 1000000
	return fmt.Sprintf("%03d %03d", value/1000, value%1000)
}

func LANAddress(ip net.IP, port int) string {
	return "https://" + net.JoinHostPort(ip.String(), strconv.Itoa(port))
}

// ServeLANDiscovery answers discovery questions until the context ends; current says what to
// answer and whether to answer at all, so turning visibility off silences it immediately.
func ServeLANDiscovery(ctx context.Context, conn net.PacketConn, current func() (LANBeacon, bool)) error {
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	buffer := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFrom(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("读取内网发现请求失败：%w", err)
		}
		var ask LANBeacon
		if json.Unmarshal(buffer[:n], &ask) != nil || ask.Magic != lanMagic || ask.Kind != "discover" {
			continue
		}
		reply, ok := current()
		if !ok {
			continue
		}
		reply.Magic, reply.Kind = lanMagic, "here"
		data, err := json.Marshal(reply)
		if err == nil && len(data) <= 1200 {
			_, _ = conn.WriteTo(data, from)
		}
	}
}

// LANTargets are the addresses a discovery question goes to: every IPv4 network's broadcast
// address, the limited broadcast, and this computer itself.
func LANTargets(port int) []net.Addr {
	targets := []net.Addr{&net.UDPAddr{IP: net.IPv4bcast, Port: port}, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}}
	interfaces, _ := net.Interfaces()
	for _, item := range interfaces {
		if item.Flags&net.FlagUp == 0 || item.Flags&net.FlagBroadcast == 0 || item.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, _ := item.Addrs()
		for _, address := range addresses {
			network, ok := address.(*net.IPNet)
			if !ok || network.IP.To4() == nil {
				continue
			}
			ip, mask := network.IP.To4(), network.Mask
			if len(mask) == net.IPv6len {
				mask = mask[12:]
			}
			broadcast := make(net.IP, 4)
			for index := range broadcast {
				broadcast[index] = ip[index] | ^mask[index]
			}
			targets = append(targets, &net.UDPAddr{IP: broadcast, Port: port})
		}
	}
	return targets
}

// DiscoverLAN asks the targets and collects answers until the deadline.
func DiscoverLAN(conn net.PacketConn, targets []net.Addr, deadline time.Time) ([]LANPeer, error) {
	question, _ := json.Marshal(LANBeacon{Magic: lanMagic, Kind: "discover"})
	sent := 0
	for _, target := range targets {
		if _, err := conn.WriteTo(question, target); err == nil {
			sent++
		}
	}
	if sent == 0 {
		return nil, fmt.Errorf("无法发送内网发现请求，请检查网络连接")
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return nil, fmt.Errorf("设置内网发现等待时间失败：%w", err)
	}
	found := map[string]LANPeer{}
	buffer := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFrom(buffer)
		if err != nil {
			break
		}
		var answer LANBeacon
		address, ok := from.(*net.UDPAddr)
		if !ok || json.Unmarshal(buffer[:n], &answer) != nil || answer.Magic != lanMagic || answer.Kind != "here" || answer.Port < 1 || answer.Port > 65535 || len(answer.Fingerprint) != 16 {
			continue
		}
		if _, err := hex.DecodeString(answer.Fingerprint); err != nil {
			continue
		}
		name := strings.TrimSpace(answer.Name)
		if len([]rune(name)) > 64 {
			name = string([]rune(name)[:64])
		}
		peer := LANPeer{Name: name, Address: LANAddress(address.IP, answer.Port), Fingerprint: strings.ToUpper(answer.Fingerprint)}
		// One computer can answer on several interfaces; the loopback answer is the least useful to share.
		previous, seen := found[peer.Fingerprint]
		if !seen || !address.IP.IsLoopback() && strings.Contains(previous.Address, "127.0.0.1") {
			found[peer.Fingerprint] = peer
		}
	}
	peers := make([]LANPeer, 0, len(found))
	for _, peer := range found {
		peers = append(peers, peer)
	}
	slices.SortFunc(peers, func(a, b LANPeer) int { return strings.Compare(a.Name+a.Address, b.Name+b.Address) })
	return peers, nil
}

// FetchTeamAuthority reads a monitor's public CA without trusting it yet; the caller compares
// its fingerprint with what discovery or the person said before sending anything private.
func FetchTeamAuthority(ctx context.Context, address string, connector TeamConnector, now func() time.Time) ([]byte, string, error) {
	if connector == nil || now == nil {
		return nil, "", fmt.Errorf("内网连接需要连接器和时钟")
	}
	var response TeamAuthorityResponse
	if err := connector.Exchange(ctx, address, "/team/v1/authority", &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, Time: now}, nil, &response); err != nil {
		return nil, "", err
	}
	ca, err := parseCertificate(response.CAPEM)
	if err != nil || !ca.IsCA || ca.CheckSignatureFrom(ca) != nil || now().Before(ca.NotBefore) || !now().Before(ca.NotAfter) {
		return nil, "", fmt.Errorf("对方的证书无效或已过期")
	}
	digest := sha256.Sum256(ca.Raw)
	return response.CAPEM, strings.ToUpper(hex.EncodeToString(digest[:])), nil
}

// PinnedTLS trusts exactly one CA. Local network addresses change, so the server certificate is
// checked against the pinned CA and its address list rather than a name from public DNS.
func PinnedTLS(caPEM []byte, now func() time.Time) (*tls.Config, error) {
	ca, err := parseCertificate(caPEM)
	if err != nil || !ca.IsCA {
		return nil, fmt.Errorf("对方的证书无效")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Time: now}, nil
}

// NewLANIdentity makes a device key and certificate request for a first request to someone.
func NewLANIdentity(random io.Reader) (string, *ecdsa.PrivateKey, []byte, []byte, error) {
	id, err := NewTeamID("member", random)
	if err != nil {
		return "", nil, nil, nil, err
	}
	key, csr, err := NewClientRequest(id, random)
	if err != nil {
		return "", nil, nil, nil, err
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", nil, nil, nil, fmt.Errorf("编码设备公钥失败：%w", err)
	}
	return id, key, csr, public, nil
}

// SaveTeamKey keeps a device key in the protected key store and returns its reference.
func SaveTeamKey(ctx context.Context, keys KeyStore, random io.Reader, key *ecdsa.PrivateKey) (string, error) {
	return saveKey(ctx, keys, random, key)
}

// MemberPublicKey reads the public key of an issued member certificate in PEM form.
func MemberPublicKey(certificatePEM []byte) ([]byte, error) {
	leaf, err := parseCertificate(certificatePEM)
	if err != nil {
		return nil, err
	}
	return x509.MarshalPKIXPublicKey(leaf.PublicKey)
}

// RequestPublicKey reads the public key of a certificate request.
func RequestPublicKey(der []byte) (string, []byte, error) {
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		return "", nil, fmt.Errorf("证书申请无效：%w", err)
	}
	public, err := x509.MarshalPKIXPublicKey(csr.PublicKey)
	if err != nil {
		return "", nil, fmt.Errorf("证书申请公钥无效：%w", err)
	}
	return csr.Subject.CommonName, public, nil
}
