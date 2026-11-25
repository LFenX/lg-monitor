// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
)

// Giving boards to people who may already view others: the owner names the boards and the people, and each viewer's
// lgmon asks the owners it knows which boards were given to it, so nobody has to be found and asked again.

const (
	sharedEvery      = 15 * time.Second
	sharedRetry      = time.Minute
	sharedOutdated   = 10 * time.Minute
	sharedTimeout    = 4 * time.Second
	sharedMaxBoards  = 200
	lanMaxGrantBatch = 100
)

// lanViewer is one person with every identity they paired with; members[0] is the one they used last.
type lanViewer struct {
	api.LANViewerItem
	members []string
}

func (m *TeamManager) lanViewers(ctx context.Context, repository lanRepository) ([]lanViewer, error) {
	grants, err := repository.LANGrants(ctx)
	if err != nil || len(grants) == 0 {
		return nil, err
	}
	members, err := m.options.Repository.TeamMembers(ctx, lanTeamID)
	if err != nil && !errors.Is(err, store.ErrTeamNotFound) {
		return nil, err
	}
	active := map[string]int64{}
	for _, member := range members {
		if member.RevokedAt == 0 {
			active[member.ID] = member.LastAccess
		}
	}
	known := m.peopleByHost(ctx, repository)
	result, index := []lanViewer{}, map[string]int{}
	for _, grant := range grants {
		if _, ok := active[grant.MemberID]; !ok {
			continue
		}
		// Only what the viewer said about itself joins two identities; the address fallback is for display.
		key := grant.Fingerprint
		if key == "" {
			key = grant.MemberID
		}
		at, ok := index[key]
		if !ok {
			at = len(result)
			index[key] = at
			item := api.LANViewerItem{Name: grant.DeviceName, Avatar: grant.Avatar, Fingerprint: grant.Fingerprint, Boards: []string{}}
			if person, ok := known[grant.MemberID]; ok && grant.Fingerprint == "" {
				item.Name, item.Avatar, item.Fingerprint = person.OwnerName, person.OwnerAvatar, person.Fingerprint
			}
			result = append(result, lanViewer{LANViewerItem: item})
		}
		viewer := &result[at]
		if !slices.Contains(viewer.members, grant.MemberID) {
			viewer.members = append(viewer.members, grant.MemberID)
		}
		if !slices.Contains(viewer.Boards, grant.BoardID) {
			viewer.Boards = append(viewer.Boards, grant.BoardID)
		}
	}
	for index := range result {
		viewer := &result[index]
		slices.SortStableFunc(viewer.members, func(a, b string) int {
			switch {
			case active[a] > active[b]:
				return -1
			case active[a] < active[b]:
				return 1
			}
			return 0
		})
		viewer.MemberID = viewer.members[0]
	}
	return result, nil
}

func (m *TeamManager) grantLAN(ctx context.Context, repository lanRepository, input api.LANGrantInput) (api.LANGrantResult, error) {
	result := api.LANGrantResult{Granted: []api.LANGrantItem{}}
	boardIDs, memberIDs := cleanIDs(input.BoardIDs), cleanIDs(input.MemberIDs)
	if len(boardIDs) == 0 || len(memberIDs) == 0 || len(boardIDs) > lanMaxGrantBatch || len(memberIDs) > lanMaxGrantBatch {
		return result, invalidAPI("请选择 1～100 个面板和 1～100 位已有查看权限的用户")
	}
	boards, err := m.lanBoards()
	if err != nil {
		return result, err
	}
	list, err := boards.LANBoardList(ctx)
	if err != nil {
		return result, err
	}
	names := map[string]string{}
	for _, board := range list {
		names[board.ID] = board.Name
	}
	for _, id := range boardIDs {
		if _, ok := names[id]; !ok {
			return result, missingAPI("面板 " + id + " 不存在或没有开放到内网，没有改动任何授权")
		}
	}
	viewers, err := m.lanViewers(ctx, repository)
	if err != nil {
		return result, err
	}
	chosen := map[string]bool{}
	var wanted []store.LANGrant
	for _, id := range memberIDs {
		at := slices.IndexFunc(viewers, func(viewer lanViewer) bool { return slices.Contains(viewer.members, id) })
		if at < 0 {
			return result, &api.Fault{Status: 409, Code: "lan_viewer_missing", Message: "名单里有人已经没有查看权限，请刷新后重新选择；没有改动任何授权"}
		}
		viewer := viewers[at]
		if chosen[viewer.MemberID] {
			continue
		}
		chosen[viewer.MemberID] = true
		for _, board := range boardIDs {
			if slices.Contains(viewer.Boards, board) {
				result.Existing++
				continue
			}
			grantID, err := transport.NewTeamID("grant", m.options.Random)
			if err != nil {
				return result, err
			}
			wanted = append(wanted, store.LANGrant{ID: grantID, MemberID: viewer.MemberID, BoardID: board})
		}
	}
	added, err := repository.ShareLANBoards(ctx, lanTeamID, wanted)
	if errors.Is(err, store.ErrLANNotFound) {
		return result, &api.Fault{Status: 409, Code: "lan_viewer_missing", Message: "名单里有人刚刚被撤销了查看权限，请刷新后重新选择；没有改动任何授权"}
	}
	if err != nil {
		return result, err
	}
	result.Existing += len(wanted) - len(added)
	for _, grant := range added {
		result.Granted = append(result.Granted, api.LANGrantItem{LANGrant: grant, BoardName: names[grant.BoardID]})
	}
	m.notify()
	return result, nil
}

// serveLANShared tells a paired device which boards it was given without asking, as far as they are still offered.
func (m *TeamManager) serveLANShared(w http.ResponseWriter, r *http.Request) {
	member, known, err := m.lanMember(r)
	if err != nil || !known {
		writeTeamError(w, &api.Fault{Status: 403, Code: "lan_forbidden", Message: "这台电脑还没有被允许查看"})
		return
	}
	if !m.allowRate("lan-shared:"+member.ID, time.Second) {
		writeTeamError(w, &api.Fault{Status: 429, Code: "lan_rate_limited", Message: "刷新太频繁了，请稍后再试"})
		return
	}
	repository, err := m.lanRepository()
	if err != nil {
		writeTeamError(w, err)
		return
	}
	grants, err := repository.SharedLANGrants(r.Context(), member.ID)
	if err != nil {
		writeTeamError(w, err)
		return
	}
	reply := transport.LANShared{Owner: transport.LANOwner{Name: m.lanName(), Avatar: m.lanAvatar(), Account: m.AccountID(r.Context())}, Boards: []transport.LANSharedBoard{}}
	if len(grants) > 0 {
		boards, err := m.lanBoards()
		if err != nil {
			writeTeamError(w, err)
			return
		}
		list, err := boards.LANBoardList(r.Context())
		if err != nil {
			writeTeamError(w, &api.Fault{Status: 503, Code: "lan_unavailable", Message: "对方的面板暂时读不到"})
			return
		}
		for _, grant := range grants {
			if at := slices.IndexFunc(list, func(board api.BoardInfo) bool { return board.ID == grant.BoardID }); at >= 0 {
				reply.Boards = append(reply.Boards, transport.LANSharedBoard{Grant: grant.ID, ID: grant.BoardID, Name: list[at].Name})
			}
		}
	}
	writeTeam(w, 200, reply)
}

// askShared asks, in the background, every owner whose boards this computer views what else it was given. The list
// being read is not held up; the page reads it again within seconds.
func (m *TeamManager) askShared(ctx context.Context, repository lanRepository) {
	outgoing, err := repository.LANOutgoing(ctx)
	if err != nil {
		return
	}
	now := m.options.Now()
	for _, item := range sharedOwners(outgoing) {
		m.lan.mu.Lock()
		next, asked := m.lan.sharedAt[item.JoinedID]
		due := !asked || !now.Before(next)
		if due {
			if m.lan.sharedAt == nil {
				m.lan.sharedAt = map[string]time.Time{}
			}
			m.lan.sharedAt[item.JoinedID] = now.Add(sharedEvery)
		}
		m.lan.mu.Unlock()
		if !due {
			continue
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sharedTimeout)
			defer cancel()
			var wait time.Duration
			var remote *transport.TeamRemoteError
			switch err := m.pullShared(ctx, repository, item); {
			case err == nil || errors.As(err, &remote) && remote.Code == "lan_rate_limited":
				return
			case remote != nil && remote.Code == "team_not_found":
				// An owner before this version does not answer; it may be upgraded while this computer keeps running.
				wait = sharedOutdated
			default:
				wait = sharedRetry
			}
			m.lan.mu.Lock()
			m.lan.sharedAt[item.JoinedID] = now.Add(wait)
			m.lan.mu.Unlock()
		}()
	}
}

// sharedOwners picks one approved board per identity to reach its owner by.
func sharedOwners(outgoing []store.LANOutgoing) []store.LANOutgoing {
	result, seen := []store.LANOutgoing{}, map[string]bool{}
	for _, item := range outgoing {
		if item.Status != store.LANApproved || item.JoinedID == "" || seen[item.JoinedID] {
			continue
		}
		seen[item.JoinedID] = true
		result = append(result, item)
	}
	return result
}

func (m *TeamManager) pullShared(ctx context.Context, repository lanRepository, item store.LANOutgoing) error {
	configuration, err := m.remoteIdentity(ctx, item.JoinedID)
	if err != nil {
		return err
	}
	var reply transport.LANShared
	if err := m.options.Connector.Exchange(ctx, item.Address, "/team/v1/shared", configuration, nil, &reply); err != nil {
		return err
	}
	owner := item
	if name := strings.TrimSpace(reply.Owner.Name); name != "" && utf8.RuneCountInString(name) <= 40 {
		owner.OwnerName, owner.OwnerAvatar = name, remoteAvatar(reply.Owner.Avatar)
	}
	if account := strings.TrimSpace(reply.Owner.Account); account != "" && len(account) <= 64 {
		owner.OwnerAccount = account
	}
	changed := false
	for index, board := range reply.Boards {
		name := strings.TrimSpace(board.Name)
		if index >= sharedMaxBoards || !store.ValidTeamID(board.Grant) || !validSharedBoard(board.ID) || name == "" || utf8.RuneCountInString(name) > 200 {
			continue
		}
		id, err := transport.NewTeamID("lanout", m.options.Random)
		if err != nil {
			return err
		}
		added, err := repository.AdoptLANGrant(ctx, store.LANOutgoing{ID: id, RemoteID: board.Grant, Address: item.Address, OwnerName: owner.OwnerName, OwnerAvatar: owner.OwnerAvatar, OwnerAccount: owner.OwnerAccount, Fingerprint: item.Fingerprint, BoardID: board.ID, BoardName: name, JoinedID: item.JoinedID, CA: item.CA})
		if err != nil {
			return err
		}
		changed = changed || added
	}
	if changed {
		m.notify()
	}
	return nil
}

// validSharedBoard keeps a board ID another computer sent a plain path segment before it goes into a URL.
func validSharedBoard(id string) bool {
	if id == "" || len(id) > 160 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '~') {
			return false
		}
	}
	return true
}
