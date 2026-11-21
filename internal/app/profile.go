// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"errors"
	"strings"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/profile"
	"github.com/LFenX/lg-monitor/internal/store"
)

type profileRepository interface {
	Profile(context.Context) (store.Profile, error)
	CreateProfile(context.Context, store.Profile) (store.Profile, error)
	SaveProfile(context.Context, string, string) (store.Profile, error)
}

func (m *TeamManager) profileRepository() (profileRepository, error) {
	repository, ok := m.options.Repository.(profileRepository)
	if !ok {
		return nil, &api.Fault{Status: 503, Code: "profile_unavailable", Message: "本机账户需要新版认证库"}
	}
	return repository, nil
}

// EnsureProfile creates this computer's account the first time lgmon starts: a random avatar and
// a friendly nickname, or the LAN name an older version had set. It never uses the computer's name.
func (m *TeamManager) EnsureProfile(ctx context.Context) (store.Profile, error) {
	m.account.mu.Lock()
	defer m.account.mu.Unlock()
	if m.account.value != nil {
		return *m.account.value, nil
	}
	repository, err := m.profileRepository()
	if err != nil {
		return store.Profile{}, err
	}
	value, err := repository.Profile(ctx)
	if errors.Is(err, store.ErrNoProfile) {
		value, err = m.newProfile(ctx, repository)
	}
	if err != nil {
		return store.Profile{}, err
	}
	m.account.value = &value
	return value, nil
}

func (m *TeamManager) newProfile(ctx context.Context, repository profileRepository) (store.Profile, error) {
	avatar, err := profile.RandomAvatar(m.options.Random, "")
	if err != nil {
		return store.Profile{}, err
	}
	nickname, err := profile.CheckNickname(m.options.Config.Snapshot().Settings.LAN.Name)
	if err != nil {
		if nickname, err = profile.RandomNickname(m.options.Random); err != nil {
			return store.Profile{}, err
		}
	}
	return repository.CreateProfile(ctx, store.Profile{Nickname: nickname, Avatar: avatar})
}

func (m *TeamManager) saveProfile(ctx context.Context, input api.ProfileInput) (store.Profile, error) {
	current, err := m.EnsureProfile(ctx)
	if err != nil {
		return current, err
	}
	nickname, avatar := current.Nickname, current.Avatar
	if input.Nickname != nil {
		if nickname, err = profile.CheckNickname(*input.Nickname); err != nil {
			return current, invalidAPI(err.Error())
		}
	}
	switch {
	case input.Shuffle && input.Avatar != nil:
		return current, invalidAPI("avatar 和 shuffle 只能选一个")
	case input.Shuffle:
		if avatar, err = profile.RandomAvatar(m.options.Random, current.Avatar); err != nil {
			return current, err
		}
	case input.Avatar != nil:
		avatar = strings.TrimSpace(*input.Avatar)
		if err := profile.CheckAvatar(avatar); err != nil {
			return current, invalidAPI(err.Error())
		}
	}
	repository, err := m.profileRepository()
	if err != nil {
		return current, err
	}
	m.account.mu.Lock()
	saved, err := repository.SaveProfile(ctx, nickname, avatar)
	if err == nil {
		m.account.value = &saved
	}
	m.account.mu.Unlock()
	if err != nil {
		return current, err
	}
	m.notify()
	return saved, nil
}

// lanName is what other people see: the account's nickname. Reading the account can only fail
// on a broken auth database; then the configured name, and last the computer's name, still answer.
func (m *TeamManager) lanName() string {
	if value, err := m.EnsureProfile(context.Background()); err == nil {
		return value.Nickname
	}
	if name := strings.TrimSpace(m.options.Config.Snapshot().Settings.LAN.Name); name != "" {
		return name
	}
	return m.hostname()
}

func (m *TeamManager) lanAvatar() string {
	value, _ := m.EnsureProfile(context.Background())
	return value.Avatar
}

// remoteAvatar keeps what another computer sends only when it is a picture lgmon can draw safely.
func remoteAvatar(value string) string {
	if profile.CheckAvatar(value) != nil {
		return ""
	}
	return value
}
