// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
)

/*
 * Sharing 波西米亚's model with colleagues on the LAN. The owner picks members (people already paired through a
 * board request) who may use it; their lgmon sends each model request to the owner's member port over the paired
 * identity, and the owner's lgmon sends it on to DeepSeek with its own key. The key never leaves the owner's
 * computer, and taking the share back takes effect on the next request.
 */

// LANAssistant is the owner's half: which model it offers, and how one request goes out with the owner's key.
type LANAssistant interface {
	ModelOffer(context.Context) (string, bool)
	ForwardChat(context.Context, []byte, http.ResponseWriter) error
}

type modelShareRepository interface {
	ModelShares(context.Context) (map[string]bool, error)
	SetModelShare(context.Context, string, bool) error
}

// SharedModel is a colleague's model this computer may use, with how to reach it.
type SharedModel struct {
	api.AssistantShare
	Address string
	Config  *tls.Config
}

type sharedLookup struct {
	at    time.Time
	value *SharedModel
}

const (
	modelChatLimit = 2 << 20
	sharedFresh    = time.Minute
)

func (m *TeamManager) SetLANAssistant(assistant LANAssistant) {
	m.lan.mu.Lock()
	m.lan.assistant = assistant
	m.lan.mu.Unlock()
}

func (m *TeamManager) modelShares() (modelShareRepository, error) {
	repository, ok := m.options.Repository.(modelShareRepository)
	if !ok {
		return nil, &api.Fault{Status: 503, Code: "share_unavailable", Message: "共享模型需要新版认证库"}
	}
	return repository, nil
}

// sharedMember answers for a LAN member the owner shared the model with, or writes why not.
func (m *TeamManager) sharedMember(w http.ResponseWriter, r *http.Request) (store.TeamMember, LANAssistant, bool) {
	member, known, err := m.lanMember(r)
	if err != nil || !known {
		writeTeamError(w, &api.Fault{Status: 403, Code: "lan_forbidden", Message: "这台电脑还没有和对方配对"})
		return member, nil, false
	}
	repository, err := m.modelShares()
	if err != nil {
		writeTeamError(w, err)
		return member, nil, false
	}
	shares, err := repository.ModelShares(r.Context())
	if err != nil || !shares[member.ID] {
		writeTeamError(w, &api.Fault{Status: 403, Code: "assistant_not_shared", Message: "对方没有把模型共享给你"})
		return member, nil, false
	}
	m.lan.mu.Lock()
	assistant := m.lan.assistant
	m.lan.mu.Unlock()
	if assistant == nil {
		writeTeamError(w, &api.Fault{Status: 503, Code: "assistant_not_shared", Message: "对方的波西米亚还没有准备好"})
		return member, nil, false
	}
	return member, assistant, true
}

func (m *TeamManager) serveModelOffer(w http.ResponseWriter, r *http.Request) {
	_, assistant, ok := m.sharedMember(w, r)
	if !ok {
		return
	}
	model, available := assistant.ModelOffer(r.Context())
	if !available {
		writeTeamError(w, &api.Fault{Status: 404, Code: "assistant_not_shared", Message: "对方还没有配置自己的模型"})
		return
	}
	writeTeam(w, 200, transport.ModelOffer{Model: model, OwnerName: m.lanName(), OwnerAvatar: m.lanAvatar()})
}

func (m *TeamManager) serveModelChat(w http.ResponseWriter, r *http.Request) {
	member, assistant, ok := m.sharedMember(w, r)
	if !ok {
		return
	}
	// A conversation makes a few requests a minute; a loop making many is stopped here, not at the owner's bill.
	if !m.allowRate("lan-model:"+member.ID, 400*time.Millisecond) {
		writeTeamError(w, &api.Fault{Status: 429, Code: "lan_rate_limited", Message: "请求太频繁了，请稍后再试"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, modelChatLimit+1))
	if err != nil || len(body) > modelChatLimit {
		writeTeamError(w, invalidAPI("请求太大或不可读"))
		return
	}
	if err := assistant.ForwardChat(r.Context(), body, w); err != nil {
		var fault *api.Fault
		if !errors.As(err, &fault) {
			fault = &api.Fault{Status: 502, Code: "assistant_unavailable", Message: "对方的模型暂时不可用"}
		}
		writeTeamError(w, fault)
	}
}

// SharedModel finds a colleague's model this computer may use, asking each paired owner once a minute at most.
func (m *TeamManager) SharedModel(ctx context.Context) (*SharedModel, error) {
	m.lan.mu.Lock()
	kept := m.lan.shared
	m.lan.mu.Unlock()
	if age := m.options.Now().Sub(kept.at); !kept.at.IsZero() && age >= 0 && age < sharedFresh {
		return kept.value, nil
	}
	repository, err := m.lanRepository()
	if err != nil {
		return nil, err
	}
	outgoing, err := repository.LANOutgoing(ctx)
	if err != nil {
		return nil, err
	}
	var found *SharedModel
	asked := map[string]bool{}
	for _, item := range outgoing {
		if item.Status != store.LANApproved || item.JoinedID == "" || asked[item.Fingerprint] {
			continue
		}
		asked[item.Fingerprint] = true
		configuration, err := m.remoteIdentity(ctx, item.JoinedID)
		if err != nil {
			continue
		}
		look, cancel := context.WithTimeout(ctx, 4*time.Second)
		var offer transport.ModelOffer
		err = m.options.Connector.Exchange(look, item.Address, "/team/v1/assistant", configuration, nil, &offer)
		cancel()
		if err != nil || strings.TrimSpace(offer.Model) == "" {
			continue
		}
		name := firstOf(strings.TrimSpace(offer.OwnerName), item.OwnerName)
		found = &SharedModel{AssistantShare: api.AssistantShare{Name: name, Avatar: remoteAvatar(offer.OwnerAvatar), Model: offer.Model}, Address: item.Address, Config: configuration}
		break
	}
	m.lan.mu.Lock()
	m.lan.shared = sharedLookup{at: m.options.Now(), value: found}
	m.lan.mu.Unlock()
	return found, nil
}

// modelShareList is the owner's view: every paired colleague, and whether each may use the model.
func (m *TeamManager) modelShareList(ctx context.Context) ([]api.ModelShareItem, error) {
	shares, err := m.modelShares()
	if err != nil {
		return nil, err
	}
	shared, err := shares.ModelShares(ctx)
	if err != nil {
		return nil, err
	}
	repository, err := m.lanRepository()
	if err != nil {
		return nil, err
	}
	grants, err := repository.LANGrants(ctx)
	if err != nil {
		return nil, err
	}
	result := []api.ModelShareItem{}
	for _, grant := range grants {
		if slices.ContainsFunc(result, func(item api.ModelShareItem) bool { return item.MemberID == grant.MemberID }) {
			continue
		}
		result = append(result, api.ModelShareItem{MemberID: grant.MemberID, Name: grant.DeviceName, Avatar: grant.Avatar, Shared: shared[grant.MemberID]})
	}
	return result, nil
}

func (m *TeamManager) setModelShare(ctx context.Context, member string, input api.ModelShareInput) (api.ModelShareItem, error) {
	list, err := m.modelShareList(ctx)
	if err != nil {
		return api.ModelShareItem{}, err
	}
	index := slices.IndexFunc(list, func(item api.ModelShareItem) bool { return item.MemberID == member })
	if index < 0 {
		return api.ModelShareItem{}, missingAPI("只能共享给已经获准查看你面板的同事")
	}
	shares, _ := m.modelShares()
	if err := shares.SetModelShare(ctx, member, input.Shared); err != nil {
		return api.ModelShareItem{}, err
	}
	list[index].Shared = input.Shared
	return list[index], nil
}
