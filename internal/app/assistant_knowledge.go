package app

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/docs"
	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
)

/*
 * 波西米亚 answers "how does lgmon do X" from lgmon's own guides. The guides travel inside the binary, are cut
 * at their headings, embedded once with 百炼 and kept in the data folder; a question is embedded, the closest
 * passages are reranked by 百炼, and the best few go to the model with their source.
 */

const (
	helpChunkRunes = 900
	helpCandidates = 20
	helpAnswers    = 5
	embedBatch     = 10
)

type helpChunk struct {
	Doc, Title, Text string
}

type helpHit struct {
	Doc     string  `json:"doc"`
	Section string  `json:"section"`
	Text    string  `json:"text"`
	Score   float64 `json:"score"`
}

// guideTitles names each guide the way a person would.
var guideTitles = map[string]string{
	"web-panel.md": "网页使用说明", "agent-guide.md": "Agent 指南", "teams.md": "内网共享", "mcp.md": "MCP 工具",
	"rules.md": "告警规则", "cli.md": "命令行", "card-templates.md": "飞书卡片模板", "dashboards.md": "自定义视图",
	"collection.md": "采集", "service-switch.md": "服务开关导入", "windows-service.md": "Windows 服务",
	"installer.md": "安装", "webhooks.md": "Webhook", "multi-monitor.md": "多台电脑",
}

// helpChunks cuts every guide at its headings, then long sections at paragraph breaks.
func helpChunks() ([]helpChunk, error) {
	var chunks []helpChunk
	names, err := fs.Glob(docs.Guides, "*.md")
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	for _, name := range names {
		data, err := fs.ReadFile(docs.Guides, name)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, chunkGuide(cmp.Or(guideTitles[name], name), data)...)
	}
	return chunks, nil
}

func chunkGuide(doc string, data []byte) []helpChunk {
	// A Windows checkout (core.autocrlf) embeds the guides with CRLF; paragraphs must still split.
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	var chunks []helpChunk
	var heads []string
	var body strings.Builder
	flush := func() {
		text := strings.TrimSpace(body.String())
		body.Reset()
		if utf8.RuneCountInString(text) < 40 {
			return
		}
		title := strings.Join(heads, " / ")
		for _, piece := range splitRunes(text, helpChunkRunes) {
			chunks = append(chunks, helpChunk{Doc: doc, Title: title, Text: piece})
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if level := headingLevel(line); level > 0 {
			flush()
			if level > len(heads)+1 {
				level = len(heads) + 1
			}
			heads = append(heads[:level-1], strings.TrimSpace(strings.TrimLeft(line, "#")))
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	flush()
	return chunks
}

func headingLevel(line string) int {
	level := 0
	for level < len(line) && level < 6 && line[level] == '#' {
		level++
	}
	if level == 0 || level >= len(line) || line[level] != ' ' {
		return 0
	}
	return level
}

func splitRunes(text string, limit int) []string {
	if utf8.RuneCountInString(text) <= limit {
		return []string{text}
	}
	var pieces []string
	var current strings.Builder
	for _, paragraph := range strings.Split(text, "\n\n") {
		if current.Len() > 0 && utf8.RuneCountInString(current.String())+utf8.RuneCountInString(paragraph) > limit {
			pieces = append(pieces, strings.TrimSpace(current.String()))
			current.Reset()
		}
		for utf8.RuneCountInString(paragraph) > limit {
			runes := []rune(paragraph)
			pieces = append(pieces, string(runes[:limit]))
			paragraph = string(runes[limit:])
		}
		current.WriteString(paragraph)
		current.WriteString("\n\n")
	}
	if strings.TrimSpace(current.String()) != "" {
		pieces = append(pieces, strings.TrimSpace(current.String()))
	}
	return pieces
}

func chunkKey(model string, chunk helpChunk) string {
	sum := sha256.Sum256([]byte(model + "\x00" + chunk.Title + "\x00" + chunk.Text))
	return hex.EncodeToString(sum[:16])
}

// helpIndex keeps the vectors in memory and on disk, keyed by model and passage, so a new version of the guides
// only embeds what changed.
type helpIndex struct {
	mu      sync.Mutex
	path    string
	model   string
	vectors map[string][]float32
	loaded  bool
}

func (x *helpIndex) load() {
	if x.loaded {
		return
	}
	x.loaded = true
	x.vectors = map[string][]float32{}
	if x.path == "" {
		return
	}
	data, err := os.ReadFile(x.path)
	if err != nil {
		return
	}
	var stored struct {
		Model   string               `json:"model"`
		Vectors map[string][]float32 `json:"vectors"`
	}
	if json.Unmarshal(data, &stored) == nil && stored.Model == x.model {
		x.vectors = stored.Vectors
	}
}

func (x *helpIndex) save() error {
	if x.path == "" {
		return nil
	}
	data, err := json.Marshal(map[string]any{"model": x.model, "vectors": x.vectors})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(x.path), 0o700); err != nil {
		return err
	}
	temp := x.path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, x.path)
}

// dashScope talks to 百炼's embedding and rerank endpoints with one key.
type dashScope struct {
	base, key, embedding, rerank string
	client                       *http.Client
}

func (d dashScope) post(ctx context.Context, path string, body any, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, d.base+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+d.key)
	response, err := d.client.Do(request)
	if err != nil {
		return networkFailure(err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if response.StatusCode >= 300 {
		return dashScopeFailure(response.StatusCode, payload)
	}
	return json.Unmarshal(payload, out)
}

func dashScopeFailure(status int, payload []byte) error {
	cause := fmt.Errorf("HTTP %d: %s", status, strings.TrimSpace(string(payload[:min(len(payload), 400)])))
	switch {
	case status == 401 || status == 403:
		return &api.Fault{Status: 502, Code: "dashscope_key_rejected", Message: "百炼拒绝了这个 API Key，请到百炼控制台复制有效的密钥", Cause: cause}
	case status == 404 || strings.Contains(string(payload), "Model.NotFound") || strings.Contains(string(payload), "model_not_found"):
		return &api.Fault{Status: 502, Code: "dashscope_model_missing", Message: "百炼找不到这个模型，请检查向量模型和 rerank 模型的名字，以及地域是否开通", Cause: cause}
	case status == 429:
		return &api.Fault{Status: 502, Code: "dashscope_rate_limited", Message: "百炼暂时限流或额度不足，请稍后再试", Cause: cause}
	}
	return &api.Fault{Status: 502, Code: "dashscope_failed", Message: "百炼没有接受这次请求（HTTP " + fmt.Sprint(status) + "）", Cause: cause}
}

func (d dashScope) embed(ctx context.Context, texts []string) ([][]float32, error) {
	var result struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := d.post(ctx, "/compatible-mode/v1/embeddings", map[string]any{"model": d.embedding, "input": texts, "encoding_format": "float"}, &result); err != nil {
		return nil, err
	}
	vectors := make([][]float32, len(texts))
	for _, item := range result.Data {
		if item.Index >= 0 && item.Index < len(vectors) {
			vectors[item.Index] = item.Embedding
		}
	}
	for _, vector := range vectors {
		if len(vector) == 0 {
			return nil, &api.Fault{Status: 502, Code: "dashscope_failed", Message: "百炼返回的向量不完整"}
		}
	}
	return vectors, nil
}

func (d dashScope) rerankScores(ctx context.Context, query string, documents []string) ([]float64, error) {
	var result struct {
		Output struct {
			Results []struct {
				Index int     `json:"index"`
				Score float64 `json:"relevance_score"`
			} `json:"results"`
		} `json:"output"`
	}
	body := map[string]any{"model": d.rerank, "input": map[string]any{"query": query, "documents": documents}, "parameters": map[string]any{"top_n": len(documents), "return_documents": false}}
	if err := d.post(ctx, "/api/v1/services/rerank/text-rerank/text-rerank", body, &result); err != nil {
		return nil, err
	}
	scores := make([]float64, len(documents))
	for _, item := range result.Output.Results {
		if item.Index >= 0 && item.Index < len(scores) {
			scores[item.Index] = item.Score
		}
	}
	return scores, nil
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for index := range min(len(a), len(b)) {
		dot += float64(a[index]) * float64(b[index])
		na += float64(a[index]) * float64(a[index])
		nb += float64(b[index]) * float64(b[index])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// keywordScore is the fallback when 百炼 cannot be reached: Chinese is matched by character pairs, other words whole.
func keywordScore(query, text string) float64 {
	text = strings.ToLower(text)
	score := 0.0
	for _, term := range queryTerms(query) {
		if strings.Contains(text, term) {
			score += float64(utf8.RuneCountInString(term))
		}
	}
	return score
}

func queryTerms(query string) []string {
	var terms []string
	var word []rune
	runes := []rune(strings.ToLower(query))
	for index, r := range runes {
		if unicode.Is(unicode.Han, r) {
			if index+1 < len(runes) && unicode.Is(unicode.Han, runes[index+1]) {
				terms = append(terms, string(runes[index:index+2]))
			}
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word = append(word, r)
			continue
		}
		if len(word) > 1 {
			terms = append(terms, string(word))
		}
		word = word[:0]
	}
	if len(word) > 1 {
		terms = append(terms, string(word))
	}
	return terms
}

// searchHelp returns the passages of lgmon's guides that answer a question best, and a note when it had to fall
// back to plain keyword matching.
func (b *APIBackend) searchHelp(ctx context.Context, settings config.AssistantSettings, query string) ([]helpHit, string, error) {
	chunks, err := helpChunks()
	if err != nil {
		return nil, "", err
	}
	scores := make([]float64, len(chunks))
	note := ""
	client, keyErr := b.dashScope(ctx, settings)
	if keyErr == nil {
		if err := b.ensureHelpIndex(ctx, client, chunks); err != nil {
			keyErr = err
		}
	}
	var question []float32
	if keyErr == nil {
		vectors, err := client.embed(ctx, []string{query})
		if err != nil {
			keyErr = err
		} else {
			question = vectors[0]
		}
	}
	if keyErr == nil {
		b.help.mu.Lock()
		for index, chunk := range chunks {
			scores[index] = cosine(question, b.help.vectors[chunkKey(client.embedding, chunk)])
		}
		b.help.mu.Unlock()
	} else {
		var fault *api.Fault
		if errors.As(keyErr, &fault) {
			note = "百炼暂时不可用（" + fault.Message + "），这次按关键词查找说明。"
		} else {
			note = "百炼暂时不可用，这次按关键词查找说明。"
		}
		for index, chunk := range chunks {
			scores[index] = keywordScore(query, chunk.Title+" "+chunk.Text)
		}
	}
	order := make([]int, len(chunks))
	for index := range order {
		order[index] = index
	}
	slices.SortStableFunc(order, func(a, c int) int { return cmp.Compare(scores[c], scores[a]) })
	order = order[:min(len(order), helpCandidates)]
	if keyErr == nil && len(order) > 1 {
		documents := make([]string, len(order))
		for index, at := range order {
			documents[index] = chunks[at].Title + "\n" + chunks[at].Text
		}
		if reranked, err := client.rerankScores(ctx, query, documents); err == nil {
			for index, at := range order {
				scores[at] = reranked[index]
			}
			slices.SortStableFunc(order, func(a, c int) int { return cmp.Compare(scores[c], scores[a]) })
		} else {
			note = "这次没能用百炼重新排序，按向量相似度给出结果。"
		}
	}
	hits := []helpHit{}
	for _, at := range order[:min(len(order), helpAnswers)] {
		if scores[at] <= 0 {
			continue
		}
		hits = append(hits, helpHit{Doc: chunks[at].Doc, Section: chunks[at].Title, Text: chunks[at].Text, Score: math.Round(scores[at]*1000) / 1000})
	}
	return hits, note, nil
}

func (b *APIBackend) ensureHelpIndex(ctx context.Context, client dashScope, chunks []helpChunk) error {
	b.help.mu.Lock()
	defer b.help.mu.Unlock()
	if b.help.model != client.embedding {
		b.help.model, b.help.loaded = client.embedding, false
	}
	b.help.path = b.helpPath()
	b.help.load()
	var missing []int
	for index, chunk := range chunks {
		if _, ok := b.help.vectors[chunkKey(client.embedding, chunk)]; !ok {
			missing = append(missing, index)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	build, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for start := 0; start < len(missing); start += embedBatch {
		batch := missing[start:min(len(missing), start+embedBatch)]
		texts := make([]string, len(batch))
		for index, at := range batch {
			texts[index] = chunks[at].Title + "\n" + chunks[at].Text
		}
		vectors, err := client.embed(build, texts)
		if err != nil {
			_ = b.help.save()
			return err
		}
		for index, at := range batch {
			b.help.vectors[chunkKey(client.embedding, chunks[at])] = vectors[index]
		}
	}
	// Drop passages the guides no longer contain.
	keep := make(map[string]bool, len(chunks))
	for _, chunk := range chunks {
		keep[chunkKey(client.embedding, chunk)] = true
	}
	for key := range b.help.vectors {
		if !keep[key] {
			delete(b.help.vectors, key)
		}
	}
	return b.help.save()
}

// helpIndexed reports how many passages are ready, for the settings page.
func (b *APIBackend) helpIndexed(settings config.AssistantSettings) int {
	b.help.mu.Lock()
	defer b.help.mu.Unlock()
	model := settings.EffectiveEmbeddingModel()
	if b.help.model != model {
		b.help.model, b.help.loaded = model, false
	}
	b.help.path = b.helpPath()
	b.help.load()
	return len(b.help.vectors)
}

func (b *APIBackend) helpPath() string {
	if b.options.Home == "" {
		return ""
	}
	return filepath.Join(b.options.Home, "data", "assistant-index.json")
}
