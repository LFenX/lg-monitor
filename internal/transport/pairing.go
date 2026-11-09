package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/pipeline"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type PairService struct {
	lgmonv1.UnimplementedPairingServer
	authority   *Authority
	repository  PairRepository
	revoke      func(string)
	configState func(string) CollectorConfigState
	cacheState  func(string) *pipeline.CacheUsage
	serviceTest func(context.Context, *lgmonv1.ServiceTestRequest) (*lgmonv1.ServiceTestResponse, error)
	teamAdmin   func(context.Context, *lgmonv1.TeamAdminRequest) ([]byte, error)
}

func NewSecureServer(authority *Authority, repository PairRepository, register func(context.Context, Hello) error, resume ...func(context.Context, string) (uint64, error)) (*Receiver, *grpc.Server) {
	receiver := NewReceiver(register, func(ctx context.Context, hello Hello) error {
		return authority.AuthorizeCollector(ctx, repository, hello)
	})
	receiver.now = authority.options.Now
	if len(resume) > 0 {
		receiver.resume = resume[0]
	}
	server := NewServer(receiver, grpc.Creds(credentials.NewTLS(authority.ServerTLS(repository))))
	lgmonv1.RegisterPairingServer(server, &PairService{authority: authority, repository: repository, revoke: receiver.Revoke, configState: receiver.ConfigState, cacheState: receiver.CacheUsage,
		teamAdmin: func(ctx context.Context, request *lgmonv1.TeamAdminRequest) ([]byte, error) {
			if receiver.teamAdmin == nil {
				return nil, status.Error(codes.FailedPrecondition, "团队管理尚未启用，请确认监控器版本")
			}
			return receiver.teamAdmin(ctx, request)
		},
		serviceTest: func(ctx context.Context, request *lgmonv1.ServiceTestRequest) (*lgmonv1.ServiceTestResponse, error) {
			if receiver.serviceTest == nil {
				return nil, status.Error(codes.FailedPrecondition, "监控器未以 LocalSystem Windows 服务运行；collector 角色不保存飞书凭据")
			}
			return receiver.serviceTest(ctx, request)
		}})
	return receiver, server
}

func (a *Authority) ServerTLS(repository PairRepository) *tls.Config {
	roots := x509.NewCertPool()
	roots.AddCert(a.ca)
	base := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{a.server}, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: roots, Time: a.options.Now}
	base.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		configuration := base.Clone()
		configuration.GetConfigForClient = nil
		configuration.VerifyConnection = func(connection tls.ConnectionState) error {
			if len(connection.PeerCertificates) == 0 {
				return nil
			}
			leaf := connection.PeerCertificates[0]
			if bytes.Equal(leaf.Raw, a.admin.Leaf.Raw) {
				return nil
			}
			item, found, err := repository.Find(hello.Context(), leaf.Subject.CommonName)
			if err != nil || !found || item.Revoked || item.Serial != leaf.SerialNumber.String() {
				return fmt.Errorf("采集器证书未登记或已吊销")
			}
			return nil
		}
		return configuration, nil
	}
	return base
}

func verifiedLeaf(ctx context.Context) (*x509.Certificate, error) {
	remote, ok := peer.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "需要双向 TLS 证书")
	}
	info, ok := remote.AuthInfo.(credentials.TLSInfo)
	if !ok || len(info.State.PeerCertificates) == 0 || len(info.State.VerifiedChains) == 0 {
		return nil, status.Error(codes.Unauthenticated, "需要可信客户端证书")
	}
	return info.State.PeerCertificates[0], nil
}

func (a *Authority) AuthorizeCollector(ctx context.Context, repository PairRepository, hello Hello) error {
	leaf, err := verifiedLeaf(ctx)
	if err != nil {
		return err
	}
	if leaf.Subject.CommonName != hello.CollectorID || leaf.Subject.CommonName == adminName {
		return status.Error(codes.PermissionDenied, "采集器证书与 Hello 身份不符")
	}
	item, found, err := repository.Find(ctx, hello.CollectorID)
	if err != nil {
		return status.Error(codes.Unavailable, "读取采集器授权失败")
	}
	if !found || item.Revoked || item.Serial != leaf.SerialNumber.String() {
		return status.Error(codes.PermissionDenied, "采集器证书未登记或已吊销")
	}
	return nil
}

func (s *PairService) admin(ctx context.Context) error {
	leaf, err := verifiedLeaf(ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(leaf.Raw, s.authority.admin.Leaf.Raw) {
		return status.Error(codes.PermissionDenied, "此操作需要本机管理证书")
	}
	return nil
}

func (s *PairService) Authority(context.Context, *lgmonv1.AuthorityRequest) (*lgmonv1.AuthorityResponse, error) {
	return &lgmonv1.AuthorityResponse{CaPem: s.authority.CAPEM()}, nil
}

func normalizedCode(code string) string {
	return strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(code)), "-", "")
}
func codeHash(code string) string {
	sum := sha256.Sum256([]byte(normalizedCode(code)))
	return hex.EncodeToString(sum[:])
}

func newCode(random io.Reader) (string, error) {
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	data := make([]byte, 12)
	if _, err := io.ReadFull(random, data); err != nil {
		return "", fmt.Errorf("生成配对码失败：%w", err)
	}
	for i := range data {
		data[i] = alphabet[int(data[i])&31]
	}
	return string(data[:4]) + "-" + string(data[4:8]) + "-" + string(data[8:]), nil
}

func (s *PairService) NewCode(ctx context.Context, request *lgmonv1.NewCodeRequest) (*lgmonv1.NewCodeResponse, error) {
	if err := s.admin(ctx); err != nil {
		return nil, err
	}
	if request.TtlMillis < 1000 || request.TtlMillis > int64((24*time.Hour)/time.Millisecond) {
		return nil, status.Error(codes.InvalidArgument, "配对码有效期须为 1 秒～24 小时")
	}
	code, err := newCode(s.authority.options.Random)
	if err != nil {
		return nil, status.Error(codes.Internal, "生成配对码失败")
	}
	expires := s.authority.options.Now().Add(time.Duration(request.TtlMillis) * time.Millisecond).UnixMilli()
	if err := s.repository.AddCode(ctx, codeHash(code), expires); err != nil {
		return nil, status.Error(codes.Unavailable, "保存配对码失败")
	}
	return &lgmonv1.NewCodeResponse{Code: code, ExpiresAt: expires, Fingerprint: s.authority.Fingerprint()}, nil
}

func (s *PairService) Join(ctx context.Context, request *lgmonv1.JoinRequest) (*lgmonv1.JoinResponse, error) {
	if len(normalizedCode(request.Code)) != 12 || len(request.Hostname) > 255 {
		return nil, status.Error(codes.InvalidArgument, "配对码或主机名无效")
	}
	id, certificate, err := s.authority.SignCSR(request.CsrDer)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "证书申请格式、签名或身份无效")
	}
	leaf, err := parseCertificate(certificate)
	if err != nil {
		return nil, status.Error(codes.Internal, "签发客户端证书失败")
	}
	credential := Credential{ID: id, Hostname: request.Hostname, Serial: leaf.SerialNumber.String(), IssuedAt: s.authority.options.Now().UnixMilli()}
	if err := s.repository.Consume(ctx, codeHash(request.Code), s.authority.options.Now().UnixMilli(), credential); err != nil {
		if errors.Is(err, ErrPairCode) {
			return nil, status.Error(codes.PermissionDenied, ErrPairCode.Error())
		}
		if errors.Is(err, ErrRegistered) {
			return nil, status.Error(codes.AlreadyExists, ErrRegistered.Error())
		}
		return nil, status.Error(codes.Unavailable, "配对事务未完成，请检查监控器后重试")
	}
	return &lgmonv1.JoinResponse{CaPem: s.authority.CAPEM(), CertificatePem: certificate}, nil
}

func (s *PairService) ListCollectors(ctx context.Context, _ *lgmonv1.ListCollectorsRequest) (*lgmonv1.ListCollectorsResponse, error) {
	if err := s.admin(ctx); err != nil {
		return nil, err
	}
	items, err := s.repository.List(ctx)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "读取配对采集器失败")
	}
	result := &lgmonv1.ListCollectorsResponse{Collectors: make([]*lgmonv1.CollectorIdentity, 0, len(items))}
	for _, item := range items {
		state := AwaitingCollectorConfig()
		var cache *lgmonv1.CollectorCache
		if s.cacheState != nil {
			if usage := s.cacheState(item.ID); usage != nil {
				cache = &lgmonv1.CollectorCache{Bytes: usage.Bytes, MaxBytes: usage.MaxBytes, BytesPerHour: usage.BytesPerHour, ProjectedHours: usage.ProjectedHours}
			}
		}
		if s.configState != nil {
			state = s.configState(item.ID)
		}
		result.Collectors = append(result.Collectors, &lgmonv1.CollectorIdentity{Id: item.ID, Hostname: item.Hostname, Serial: item.Serial, IssuedAt: item.IssuedAt, Revoked: item.Revoked,
			Config: &lgmonv1.CollectorConfigState{State: state.State, Version: state.Version, Message: state.Message}, Cache: cache})
	}
	return result, nil
}

func (s *PairService) RevokeCollector(ctx context.Context, request *lgmonv1.RevokeCollectorRequest) (*lgmonv1.RevokeCollectorResponse, error) {
	if err := s.admin(ctx); err != nil {
		return nil, err
	}
	if err := s.repository.Revoke(ctx, request.Id); err != nil {
		return nil, status.Error(codes.NotFound, "采集器不存在或吊销写入失败")
	}
	if s.revoke != nil {
		s.revoke(request.Id)
	}
	return &lgmonv1.RevokeCollectorResponse{}, nil
}
