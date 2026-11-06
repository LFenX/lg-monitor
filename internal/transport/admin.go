package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"
)

func ConnectAdmin(ctx context.Context, state StateStore, keys KeyStore, now func() time.Time, connector TLSConnector) (RPCConnection, error) {
	if connector == nil {
		connector = NativeConnector{}
	}
	if now == nil {
		now = time.Now
	}
	data, err := state.Load(ctx, "authority")
	if err != nil {
		return nil, fmt.Errorf("请先启动 monitor 或 both 创建证书：%w", err)
	}
	var document authorityDocument
	if err := decodeState(data, &document); err != nil {
		return nil, err
	}
	if document.Version != 1 {
		return nil, fmt.Errorf("监控器证书状态版本无效")
	}
	ca, err := parseCertificate(document.CA)
	if err != nil {
		return nil, err
	}
	identity, err := loadIdentity(ctx, keys, document.Admin, document.AdminKey, ca, x509.ExtKeyUsageClientAuth, now())
	if err != nil {
		return nil, err
	}
	if identity.Leaf.Subject.CommonName != adminName {
		return nil, fmt.Errorf("本机管理证书身份无效")
	}
	data, err = state.Load(ctx, "endpoint")
	if err != nil {
		return nil, fmt.Errorf("读取监控器地址失败，请先启动 monitor 或 both：%w", err)
	}
	var endpoint struct {
		Address string `json:"address"`
	}
	if err := decodeState(data, &endpoint); err != nil {
		return nil, err
	}
	if endpoint.Address == "" {
		return nil, fmt.Errorf("监控器地址为空，请先启动 monitor 或 both")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return connector.Connect(ctx, endpoint.Address, &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{identity}, Time: now})
}
