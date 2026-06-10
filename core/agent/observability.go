package agent

import (
	"sort"
	"sync"
	"time"
)

type EvaluationResult struct {
	Judgments    map[string]string
	Reasoning    map[string]string
	Instructions map[string]string
}

type Tagger struct {
	tags              map[string]tagEntry
	evaluationResults []map[string]any
	outcomeReason     string
	mu                sync.Mutex
}

type tagEntry struct {
	metadata  map[string]any
	timestamp time.Time
}

type TagMetadata struct {
	Name      string
	Metadata  map[string]any
	Timestamp time.Time
}

func NewTagger() *Tagger {
	return &Tagger{
		tags:              make(map[string]tagEntry),
		evaluationResults: make([]map[string]any, 0),
	}
}

func (t *Tagger) Success(reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.tags, "lk.fail")
	t.tags["lk.success"] = tagEntry{timestamp: time.Now()}
	t.outcomeReason = reason
}

func (t *Tagger) Fail(reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.tags, "lk.success")
	t.tags["lk.fail"] = tagEntry{timestamp: time.Now()}
	t.outcomeReason = reason
}

func (t *Tagger) Add(tag string, metadata ...map[string]any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := tagEntry{timestamp: time.Now()}
	if len(metadata) > 0 {
		entry.metadata = cloneTagMetadata(metadata[0])
	}
	t.tags[tag] = entry
}

func (t *Tagger) Remove(tag string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.tags, tag)
}

func (t *Tagger) Tags() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	tags := make([]string, 0, len(t.tags))
	for tag := range t.tags {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

func (t *Tagger) MetadataTags() []TagMetadata {
	t.mu.Lock()
	defer t.mu.Unlock()
	tags := make([]TagMetadata, 0)
	for name, entry := range t.tags {
		if len(entry.metadata) == 0 {
			continue
		}
		tags = append(tags, TagMetadata{Name: name, Metadata: cloneTagMetadata(entry.metadata), Timestamp: entry.timestamp})
	}
	sort.Slice(tags, func(i, j int) bool {
		return tags[i].Name < tags[j].Name
	})
	return tags
}

func (t *Tagger) OutcomeReason() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.outcomeReason
}

func (t *Tagger) Outcome() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.tags["lk.success"]; ok {
		return "success"
	}
	if _, ok := t.tags["lk.fail"]; ok {
		return "fail"
	}
	return ""
}

func (t *Tagger) Evaluations() []map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	evaluations := make([]map[string]any, len(t.evaluationResults))
	for i, evaluation := range t.evaluationResults {
		cp := make(map[string]any, len(evaluation))
		for key, value := range evaluation {
			cp[key] = value
		}
		evaluations[i] = cp
	}
	return evaluations
}

func (t *Tagger) Evaluation(result *EvaluationResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for name, verdict := range result.Judgments {
		tag := "lk.judge." + name + ":" + verdict
		reasoning := ""
		if result.Reasoning != nil {
			reasoning = result.Reasoning[name]
		}
		instructions := ""
		if result.Instructions != nil {
			instructions = result.Instructions[name]
		}
		t.tags[tag] = tagEntry{timestamp: time.Now()}
		t.evaluationResults = append(t.evaluationResults, map[string]any{
			"name":         name,
			"tag":          tag,
			"verdict":      verdict,
			"reasoning":    reasoning,
			"instructions": instructions,
		})
	}
}

func cloneTagMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	cp := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cp[key] = value
	}
	return cp
}
