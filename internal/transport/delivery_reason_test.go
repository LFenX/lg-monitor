package transport

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDeliveryReasonKeepsTheTransportMessageShort(t *testing.T) {
	refused := status.Error(codes.Unavailable, "connection error: dial tcp 127.0.0.1:7443: connectex: refused")
	if got := deliveryReason(refused); got != "connection error: dial tcp 127.0.0.1:7443: connectex: refused" {
		t.Fatalf("reason = %q", got)
	}
	if got := deliveryReason(errors.New(strings.Repeat("x", 300))); len([]rune(got)) != 201 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long reason kept %d runes", len([]rune(got)))
	}
	if deliveryReason(nil) != "" {
		t.Fatal("nil error has a reason")
	}
}
