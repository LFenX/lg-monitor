// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
)

/*
 * What 波西米亚 can look things up in, besides the live situation: lgmon's own guides, a card for every service
 * (what it is, what it watches, where it is opened, what it depends on) and the services' documents. Guides and
 * service cards are embedded as soon as 百炼 is set up; a document is embedded only after its owner agrees, and
 * until then is found by keywords alone. Every passage is keyed by its content, so editing a document embeds just
 * the changed passages and drops the old ones.
 */

var roleNames = map[string]string{
	"web": "网站", "api": "接口", "app": "应用", "database": "数据库", "cache": "缓存", "queue": "消息队列",
	"storage": "存储", "tunnel": "隧道", "gateway": "网关", "worker": "后台程序", "scheduler": "定时任务",
	"ai": "AI 服务", "mcp": "MCP 服务", "external": "外部服务",
}

var relationWords = map[string]string{
	"uses": "依赖", "calls": "调用接口", "data": "读写数据", "reads": "读取", "writes": "写入", "via": "经由",
	"forwards": "转发到", "runs": "由它触发", "sends": "发送消息到", "consumes": "消费消息自", "auth": "登录走",
	"syncs": "双向同步", "replicates": "复制到", "backup": "备份到", "fallback": "故障切换到", "monitors": "检查",
}

// docChoices is which service documents may be embedded, kept next to the index in the data folder.
type docChoices struct {
	mu        sync.Mutex
	path      string
	loaded    bool
	Asked     bool            `json:"asked"`
	Vectorize map[string]bool `json:"vectorize"`
}

func (d *docChoices) load(path string) {
	if d.loaded && d.path == path {
		return
	}
	d.path, d.loaded, d.Asked, d.Vectorize = path, true, false, map[string]bool{}
	if path == "" {
		return
	}
	if data, err := os.ReadFile(path); err == nil {
		var stored struct {
			Asked     bool            `json:"asked"`
			Vectorize map[string]bool `json:"vectorize"`
		}
		if json.Unmarshal(data, &stored) == nil {
			d.Asked = stored.Asked
			if stored.Vectorize != nil {
				d.Vectorize = stored.Vectorize
			}
		}
	}
}

func (d *docChoices) save() error {
	if d.path == "" {
		return nil
	}
	data, err := json.Marshal(map[string]any{"asked": d.Asked, "vectorize": d.Vectorize})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(d.path), 0o700); err != nil {
		return err
	}
	temp := d.path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, d.path)
}

func (b *APIBackend) docChoicesPath() string {
	if b.options.Home == "" {
		return ""
	}
	return filepath.Join(b.options.Home, "data", "assistant-docs.json")
}

// documentChoices reads whether the owner was asked, and which documents may be embedded.
func (b *APIBackend) documentChoices() (asked bool, chosen map[string]bool) {
	b.docs.mu.Lock()
	defer b.docs.mu.Unlock()
	b.docs.load(b.docChoicesPath())
	return b.docs.Asked, chosenOnly(b.docs.Vectorize)
}

func chosenOnly(value map[string]bool) map[string]bool {
	copied := make(map[string]bool, len(value))
	for key, on := range value {
		if on {
			copied[key] = true
		}
	}
	return copied
}

// setDocumentChoices records the owner's answer; documents not named keep their earlier choice.
func (b *APIBackend) setDocumentChoices(choices map[string]bool, all *bool) error {
	b.docs.mu.Lock()
	defer b.docs.mu.Unlock()
	b.docs.load(b.docChoicesPath())
	b.docs.Asked = true
	if all != nil {
		cfg := b.options.Config.Snapshot()
		for id := range cfg.Docs {
			b.docs.Vectorize[id] = *all
		}
	}
	for id, on := range choices {
		b.docs.Vectorize[id] = on
	}
	return b.docs.save()
}

// serviceCard describes one service in words a question would use; its state is left out, since it changes
// all the time and the situation already carries it.
func serviceCard(monitor api.Monitor, names map[string]string, boards map[string]string) string {
	var text strings.Builder
	text.WriteString("服务「" + monitor.Name + "」（ID " + monitor.ID + "）")
	if role := roleNames[monitor.Role]; role != "" {
		text.WriteString("，角色：" + role)
	}
	text.WriteString("。\n")
	if monitor.Description != "" {
		text.WriteString("说明：" + monitor.Description + "\n")
	}
	if len(monitor.Watching) > 0 {
		text.WriteString("监控方式：" + strings.Join(monitor.Watching, "；") + "\n")
	}
	if monitor.Entry != "" {
		text.WriteString("访问入口：" + monitor.Entry + "\n")
	}
	if monitor.Parent != "" {
		text.WriteString("是「" + firstOf(names[monitor.Parent], monitor.Parent) + "」的子服务。\n")
	}
	if len(monitor.Children) > 0 {
		kids := make([]string, 0, len(monitor.Children))
		for _, id := range monitor.Children {
			kids = append(kids, firstOf(names[id], id))
		}
		text.WriteString("子服务：" + strings.Join(kids, "、") + "\n")
	}
	for _, link := range monitor.Depends {
		line := "它" + firstOf(relationWords[link.Relation], "依赖") + "「" + firstOf(names[link.ID], link.ID) + "」"
		if link.Optional {
			line += "（可选）"
		}
		if link.Note != "" {
			line += "：" + link.Note
		}
		text.WriteString(line + "\n")
	}
	for _, link := range monitor.Dependents {
		text.WriteString("「" + firstOf(names[link.ID], link.ID) + "」" + firstOf(relationWords[link.Relation], "依赖") + "它\n")
	}
	if len(monitor.Boards) > 0 {
		shown := make([]string, 0, len(monitor.Boards))
		for _, id := range monitor.Boards {
			shown = append(shown, firstOf(boards[id], id))
		}
		text.WriteString("在面板：" + strings.Join(shown, "、") + "\n")
	}
	return strings.TrimSpace(text.String())
}

// knowledgeChunks is everything that can be searched now: guides, service cards and documents, each marked
// with whether it may be embedded.
func (b *APIBackend) knowledgeChunks(ctx context.Context, cfg *config.Config) ([]helpChunk, error) {
	chunks, err := helpChunks()
	if err != nil {
		return nil, err
	}
	for index := range chunks {
		chunks[index].Kind, chunks[index].Embed = "guide", true
	}
	monitors, err := b.monitors(ctx, cfg)
	if err != nil {
		return chunks, nil
	}
	boardList, _ := b.boardList(ctx, cfg)
	names, boards := map[string]string{}, map[string]string{}
	for _, monitor := range monitors {
		names[monitor.ID] = monitor.Name
	}
	for _, board := range boardList {
		boards[board.ID] = board.Name
	}
	for _, monitor := range monitors {
		chunks = append(chunks, helpChunk{Doc: "服务资料", Title: monitor.Name, Text: serviceCard(monitor, names, boards), Kind: "service", ID: monitor.ID, Embed: true})
	}
	_, chosen := b.documentChoices()
	ids := make([]string, 0, len(cfg.Docs))
	for id := range cfg.Docs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		for _, piece := range chunkGuide("服务文档：「"+firstOf(names[id], id)+"」", []byte(cfg.Docs[id])) {
			piece.Kind, piece.ID, piece.Embed = "doc", id, chosen[id]
			chunks = append(chunks, piece)
		}
	}
	return chunks, nil
}

// fuse ranks passages by reciprocal rank over the vector and keyword orders, so a passage either finds well
// rises. A passage that is not embedded (a document its owner kept out) counts its keyword rank twice instead of
// losing half its chance to everything that is.
func fuse(vector, keyword []float64, embedded []bool) []float64 {
	rank := func(scores []float64) []int {
		order := make([]int, len(scores))
		for index := range order {
			order[index] = index
		}
		slices.SortStableFunc(order, func(a, c int) int {
			switch {
			case scores[a] > scores[c]:
				return -1
			case scores[a] < scores[c]:
				return 1
			}
			return 0
		})
		at := make([]int, len(scores))
		for position, index := range order {
			at[index] = position
		}
		return at
	}
	fused := make([]float64, len(keyword))
	byKeyword := rank(keyword)
	var byVector []int
	if vector != nil {
		byVector = rank(vector)
	}
	for index := range fused {
		if keyword[index] > 0 {
			fused[index] += 1 / float64(60+byKeyword[index])
		}
		switch {
		case vector != nil && embedded[index] && vector[index] > 0:
			fused[index] += 1 / float64(60+byVector[index])
		case !embedded[index] && keyword[index] > 0:
			fused[index] += 1 / float64(60+byKeyword[index])
		}
	}
	return fused
}

// knowledgeState is what the settings page and the first conversation show about the library.
func (b *APIBackend) knowledgeState(ctx context.Context, cfg *config.Config) api.AssistantKnowledge {
	asked, chosen := b.documentChoices()
	state := api.AssistantKnowledge{Asked: asked, Docs: []api.KnowledgeDoc{}}
	chunks, _ := b.knowledgeChunks(ctx, cfg)
	model := cfg.Settings.Assistant.EffectiveEmbeddingModel()
	b.help.mu.Lock()
	if b.help.model != model {
		b.help.model, b.help.loaded = model, false
	}
	b.help.path = b.helpPath()
	b.help.load()
	docs := map[string]*api.KnowledgeDoc{}
	names := map[string]string{}
	if monitors, err := b.monitors(ctx, cfg); err == nil {
		for _, monitor := range monitors {
			names[monitor.ID] = monitor.Name
		}
	}
	for _, chunk := range chunks {
		_, indexed := b.help.vectors[chunkKey(model, chunk)]
		switch chunk.Kind {
		case "guide":
			state.Guides++
			if indexed {
				state.GuidesIndexed++
			}
		case "service":
			state.Services++
			if indexed {
				state.ServicesIndexed++
			}
		case "doc":
			doc := docs[chunk.ID]
			if doc == nil {
				doc = &api.KnowledgeDoc{ID: chunk.ID, Name: firstOf(names[chunk.ID], chunk.ID), Vectorize: chosen[chunk.ID]}
				docs[chunk.ID] = doc
			}
			doc.Passages++
			if indexed && chunk.Embed {
				doc.Indexed++
			}
		}
	}
	b.help.mu.Unlock()
	for _, doc := range docs {
		state.Docs = append(state.Docs, *doc)
	}
	slices.SortFunc(state.Docs, func(a, c api.KnowledgeDoc) int { return strings.Compare(a.Name, c.Name) })
	return state
}

// refreshKnowledge embeds whatever is missing in the background, for instance right after 百炼 is set up.
func (b *APIBackend) refreshKnowledge() {
	b.background.Add(1)
	go func() {
		defer b.background.Done()
		ctx, cancel := context.WithTimeout(context.Background(), contextTimeout)
		defer cancel()
		cfg := b.options.Config.Snapshot()
		client, err := b.dashScope(ctx, cfg.Settings.Assistant)
		if err != nil {
			return
		}
		if chunks, err := b.knowledgeChunks(ctx, cfg); err == nil {
			_ = b.ensureHelpIndex(ctx, client, chunks)
		}
	}()
}
