package mistralai

import (
	"context"
	"fmt"
)

type WebSearchTool struct{}

func (t *WebSearchTool) ID() string   { return "mistral_web_search" }
func (t *WebSearchTool) Name() string { return "mistral_web_search" }
func (t *WebSearchTool) Description() string {
	return "Enable web search tool to access up-to-date information."
}
func (t *WebSearchTool) Parameters() map[string]any {
	return nil
}
func (t *WebSearchTool) Execute(ctx context.Context, args string) (string, error) {
	return "dispatched", nil
}
func (t *WebSearchTool) IsProviderTool() bool { return true }

type DocumentLibraryTool struct {
	LibraryIDs []string
}

func (t *DocumentLibraryTool) ID() string          { return "mistral_document_library" }
func (t *DocumentLibraryTool) Name() string        { return "mistral_document_library" }
func (t *DocumentLibraryTool) Description() string { return "Enable document library search." }
func (t *DocumentLibraryTool) Parameters() map[string]any {
	return map[string]any{"library_ids": t.LibraryIDs}
}
func (t *DocumentLibraryTool) Execute(ctx context.Context, args string) (string, error) {
	return "dispatched", nil
}
func (t *DocumentLibraryTool) IsProviderTool() bool { return true }

type CodeInterpreterTool struct{}

func (t *CodeInterpreterTool) ID() string          { return "mistral_code_interpreter" }
func (t *CodeInterpreterTool) Name() string        { return "mistral_code_interpreter" }
func (t *CodeInterpreterTool) Description() string { return "Enable code interpreter tool." }
func (t *CodeInterpreterTool) Parameters() map[string]any {
	return nil
}
func (t *CodeInterpreterTool) Execute(ctx context.Context, args string) (string, error) {
	return "dispatched", nil
}
func (t *CodeInterpreterTool) IsProviderTool() bool { return true }

type ConnectorTool struct {
	ConnectorID string
}

func (t *ConnectorTool) ID() string {
	return fmt.Sprintf("mistral_connector_%s", t.ConnectorID)
}
func (t *ConnectorTool) Name() string        { return t.ID() }
func (t *ConnectorTool) Description() string { return "Enable connector tool." }
func (t *ConnectorTool) Parameters() map[string]any {
	return map[string]any{"connector_id": t.ConnectorID}
}
func (t *ConnectorTool) Execute(ctx context.Context, args string) (string, error) {
	return "dispatched", nil
}
func (t *ConnectorTool) IsProviderTool() bool { return true }
