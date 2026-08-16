package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/retrieval"
)

type MemoryClient interface {
	Create(context.Context, application.MutationRequest) (application.MutationResult, error)
	Update(context.Context, application.MutationRequest) (application.MutationResult, error)
	Confirm(context.Context, application.MutationRequest) (application.MutationResult, error)
	Pin(context.Context, application.MutationRequest) (application.MutationResult, error)
	Forget(context.Context, application.MutationRequest) (application.MutationResult, error)
	Restore(context.Context, application.MutationRequest) (application.MutationResult, error)
	Review(context.Context, string, string) (application.ReviewResult, error)
	Explain(context.Context, string) (application.Explanation, error)
	Search(context.Context, retrieval.SearchRequest) (retrieval.SearchResult, error)
	Get(context.Context, string, string, string, ports.FieldIdentifier) (retrieval.SearchItem, error)
}

type ContentGuard interface {
	Check(context.Context, application.OutputRequest) (application.OutputResult, error)
}

type ReviewListClient interface {
	ReviewList(context.Context, string, int, string) ([]application.ReviewResult, int, string, error)
}

// ServiceClient adapts the shared application/retrieval services for offline
// tests and daemon composition. The ordinary CLI should instead receive an
// authenticated loopback client implementing MemoryClient.
type ServiceClient struct {
	Memory    *application.MemoryService
	Retrieval *retrieval.Searcher
	ReviewFn  func(context.Context, string, string) (application.ReviewResult, error)
	ExplainFn func(context.Context, string) (application.Explanation, error)
}

func (client ServiceClient) Create(ctx context.Context, request application.MutationRequest) (application.MutationResult, error) {
	if client.Memory == nil {
		return application.MutationResult{}, errCommandUnavailable
	}
	return client.Memory.Create(ctx, request)
}
func (client ServiceClient) Update(ctx context.Context, request application.MutationRequest) (application.MutationResult, error) {
	if client.Memory == nil {
		return application.MutationResult{}, errCommandUnavailable
	}
	return client.Memory.Update(ctx, request)
}
func (client ServiceClient) Confirm(ctx context.Context, request application.MutationRequest) (application.MutationResult, error) {
	if client.Memory == nil {
		return application.MutationResult{}, errCommandUnavailable
	}
	return client.Memory.Confirm(ctx, request)
}
func (client ServiceClient) Pin(ctx context.Context, request application.MutationRequest) (application.MutationResult, error) {
	if client.Memory == nil {
		return application.MutationResult{}, errCommandUnavailable
	}
	return client.Memory.Pin(ctx, request)
}
func (client ServiceClient) Forget(ctx context.Context, request application.MutationRequest) (application.MutationResult, error) {
	if client.Memory == nil {
		return application.MutationResult{}, errCommandUnavailable
	}
	return client.Memory.Forget(ctx, request)
}
func (client ServiceClient) Restore(ctx context.Context, request application.MutationRequest) (application.MutationResult, error) {
	if client.Memory == nil {
		return application.MutationResult{}, errCommandUnavailable
	}
	return client.Memory.Restore(ctx, request)
}
func (client ServiceClient) Review(ctx context.Context, memoryID, workspaceID string) (application.ReviewResult, error) {
	if client.ReviewFn != nil {
		return client.ReviewFn(ctx, memoryID, workspaceID)
	}
	if client.Memory == nil {
		return application.ReviewResult{}, errCommandUnavailable
	}
	return client.Memory.ReviewUnverified(ctx, memoryID, workspaceID)
}
func (client ServiceClient) Explain(ctx context.Context, memoryID string) (application.Explanation, error) {
	if client.ExplainFn != nil {
		return client.ExplainFn(ctx, memoryID)
	}
	if client.Memory == nil {
		return application.Explanation{}, errCommandUnavailable
	}
	return client.Memory.Explain(ctx, memoryID)
}
func (client ServiceClient) Search(ctx context.Context, request retrieval.SearchRequest) (retrieval.SearchResult, error) {
	if client.Retrieval == nil {
		return retrieval.SearchResult{}, errCommandUnavailable
	}
	return client.Retrieval.Search(ctx, request)
}
func (client ServiceClient) Get(ctx context.Context, memoryID, workspaceID, session string, route ports.FieldIdentifier) (retrieval.SearchItem, error) {
	if client.Retrieval == nil {
		return retrieval.SearchItem{}, errCommandUnavailable
	}
	return client.Retrieval.Get(ctx, memoryID, workspaceID, session, route)
}

type MemoryCore struct {
	Client MemoryClient
	Guard  ContentGuard
	Key    func() (string, error)
}

func NewMemoryCore(client MemoryClient) *MemoryCore {
	return &MemoryCore{Client: client, Key: randomIdempotencyKey}
}

func (core *MemoryCore) Run(ctx context.Context, args []string, output Output) error {
	if core == nil || core.Client == nil {
		return errCommandUnavailable
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		return output.Result(map[string]any{"version": "talaria.memory.v1", "commands": []string{"add", "list", "review", "search", "get", "update", "confirm", "pin", "forget", "restore", "explain"}}, "memory commands: add list review search get update confirm pin forget restore explain")
	}
	flags, positional, err := parseFlags(args[1:])
	if err != nil {
		return err
	}
	switch args[0] {
	case "add":
		return core.add(ctx, flags, output)
	case "update":
		return core.update(ctx, flags, output)
	case "confirm":
		return core.confirm(ctx, flags, output)
	case "pin":
		return core.pin(ctx, flags, output)
	case "forget":
		return core.forget(ctx, flags, output)
	case "restore":
		return core.restore(ctx, flags, output)
	case "review", "list":
		if args[0] == "list" && flags["memory"] == "" && len(positional) == 0 {
			if lister, ok := core.Client.(ReviewListClient); ok {
				return core.reviewList(ctx, lister, flags, output)
			}
			return errCommandUnavailable
		}
		return core.review(ctx, flags, positional, output)
	case "search":
		return core.search(ctx, flags, output)
	case "get":
		return core.get(ctx, flags, positional, output)
	case "explain":
		return core.explain(ctx, flags, positional, output)
	default:
		return &UsageError{Message: fmt.Sprintf("unknown memory command %q", args[0])}
	}
}

func (core *MemoryCore) reviewList(ctx context.Context, lister ReviewListClient, flags map[string]string, output Output) error {
	if flags["workspace"] == "" {
		return &UsageError{Message: "memory list requires --workspace"}
	}
	limit, err := integerFlag(flags, "limit")
	if err != nil {
		return err
	}
	items, omitted, cursor, err := lister.ReviewList(ctx, flags["workspace"], limit, flags["cursor"])
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := core.guard(ctx, item.WorkspaceID, item.MemoryID, item.RevisionID, item.Title, item.Content, item.Tags); err != nil {
			return err
		}
	}
	return output.Result(map[string]any{"version": "talaria.memory-review.v1", "items": items, "included": len(items), "omitted": omitted, "next_cursor": cursor}, "review: %d included, %d omitted", len(items), omitted)
}

func (core *MemoryCore) add(ctx context.Context, flags map[string]string, output Output) error {
	idempotency, err := core.idempotency(flags)
	if err != nil {
		return err
	}
	request := application.MutationRequest{Actor: application.ActorCLI, Caller: "cli", WorkspaceID: flags["workspace"], UserGlobal: boolFlag(flags, "global"), IdempotencyKey: idempotency, Kind: domain.MemoryKind(flags["kind"]), Title: flags["title"], Content: flags["content"], Tags: splitTags(flags["tags"]), Verified: boolFlag(flags, "verified"), Pinned: boolFlag(flags, "pinned"), ResolutionState: domain.ResolutionState(flags["resolution-state"]), Provenance: domain.Provenance{Actor: "cli", Source: flags["source"], SourceLocator: flags["source-locator"]}}
	if request.Kind == "" || request.Title == "" || request.Content == "" || (!request.UserGlobal && request.WorkspaceID == "") {
		return &UsageError{Message: "memory add requires --kind, --title, --content, and --workspace (or --global)"}
	}
	result, err := core.Client.Create(ctx, request)
	if err != nil {
		return err
	}
	return output.Result(result, "created memory %s revision %s", result.MemoryID, result.RevisionID)
}

func (core *MemoryCore) update(ctx context.Context, flags map[string]string, output Output) error {
	idempotency, err := core.idempotency(flags)
	if err != nil {
		return err
	}
	request := application.MutationRequest{Actor: application.ActorCLI, Caller: "cli", MemoryID: flags["memory"], ExpectedRevisionID: flags["expected-revision"], WorkspaceID: flags["workspace"], IdempotencyKey: idempotency, Kind: domain.MemoryKind(flags["kind"]), Title: flags["title"], Content: flags["content"], Tags: splitTags(flags["tags"]), Verified: boolFlag(flags, "verified"), ResolutionState: domain.ResolutionState(flags["resolution-state"]), Provenance: domain.Provenance{Actor: "cli", Source: flags["source"], SourceLocator: flags["source-locator"]}}
	if request.MemoryID == "" || request.ExpectedRevisionID == "" {
		return &UsageError{Message: "memory update requires --memory and --expected-revision"}
	}
	result, err := core.Client.Update(ctx, request)
	if err != nil {
		return err
	}
	return output.Result(result, "updated memory %s revision %s", result.MemoryID, result.RevisionID)
}

func (core *MemoryCore) confirm(ctx context.Context, flags map[string]string, output Output) error {
	idempotency, err := core.idempotency(flags)
	if err != nil {
		return err
	}
	if flags["memory"] == "" || flags["expected-revision"] == "" {
		return &UsageError{Message: "memory confirm requires --memory and --expected-revision"}
	}
	result, err := core.Client.Confirm(ctx, application.MutationRequest{Actor: application.ActorCLI, Caller: "cli", MemoryID: flags["memory"], ExpectedRevisionID: flags["expected-revision"], IdempotencyKey: idempotency})
	if err != nil {
		return err
	}
	return output.Result(result, "confirmed memory %s revision %s", result.MemoryID, result.RevisionID)
}

func (core *MemoryCore) pin(ctx context.Context, flags map[string]string, output Output) error {
	idempotency, err := core.idempotency(flags)
	if err != nil {
		return err
	}
	if flags["memory"] == "" || flags["expected-revision"] == "" {
		return &UsageError{Message: "memory pin requires --memory and --expected-revision"}
	}
	result, err := core.Client.Pin(ctx, application.MutationRequest{Actor: application.ActorCLI, Caller: "cli", MemoryID: flags["memory"], ExpectedRevisionID: flags["expected-revision"], IdempotencyKey: idempotency})
	if err != nil {
		return err
	}
	return output.Result(result, "pinned memory %s", result.MemoryID)
}

func (core *MemoryCore) forget(ctx context.Context, flags map[string]string, output Output) error {
	idempotency, err := core.idempotency(flags)
	if err != nil {
		return err
	}
	if flags["memory"] == "" || flags["expected-revision"] == "" {
		return &UsageError{Message: "memory forget requires --memory and --expected-revision"}
	}
	result, err := core.Client.Forget(ctx, application.MutationRequest{Actor: application.ActorCLI, Caller: "cli", MemoryID: flags["memory"], ExpectedRevisionID: flags["expected-revision"], IdempotencyKey: idempotency})
	if err != nil {
		return err
	}
	return output.Result(result, "forgot memory %s revision %s", result.MemoryID, result.RevisionID)
}

func (core *MemoryCore) restore(ctx context.Context, flags map[string]string, output Output) error {
	idempotency, err := core.idempotency(flags)
	if err != nil {
		return err
	}
	if flags["memory"] == "" || flags["expected-revision"] == "" {
		return &UsageError{Message: "memory restore requires --memory and --expected-revision"}
	}
	result, err := core.Client.Restore(ctx, application.MutationRequest{Actor: application.ActorCLI, Caller: "cli", MemoryID: flags["memory"], ExpectedRevisionID: flags["expected-revision"], IdempotencyKey: idempotency, Verified: boolFlag(flags, "verified")})
	if err != nil {
		return err
	}
	return output.Result(result, "restored memory %s revision %s", result.MemoryID, result.RevisionID)
}

func (core *MemoryCore) review(ctx context.Context, flags map[string]string, positional []string, output Output) error {
	memoryID := flags["memory"]
	if memoryID == "" && len(positional) > 0 {
		memoryID = positional[0]
	}
	if memoryID == "" {
		return &UsageError{Message: "memory review requires --memory or a memory ID"}
	}
	result, err := core.Client.Review(ctx, memoryID, flags["workspace"])
	if err != nil {
		return err
	}
	if err := core.guard(ctx, result.WorkspaceID, result.MemoryID, result.RevisionID, result.Title, result.Content, result.Tags); err != nil {
		return err
	}
	return output.Result(result, "%s %s %s\n%s", result.MemoryID, result.RevisionID, result.Title, result.Content)
}

func (core *MemoryCore) search(ctx context.Context, flags map[string]string, output Output) error {
	if flags["query"] == "" || flags["workspace"] == "" {
		return &UsageError{Message: "memory search requires --query and --workspace"}
	}
	session := flags["session"]
	if session == "" {
		session, _ = randomIdempotencyKey()
	}
	limit, err := integerFlag(flags, "limit")
	if err != nil {
		return err
	}
	result, err := core.Client.Search(ctx, retrieval.SearchRequest{Query: flags["query"], WorkspaceID: flags["workspace"], ConsumerSession: session, Limit: limit, Route: ports.FieldCLIRead})
	if err != nil {
		return err
	}
	for _, item := range result.Items {
		if err := core.guard(ctx, item.WorkspaceID, item.MemoryID, item.RevisionID, item.Title, item.Content, item.Tags); err != nil {
			return err
		}
	}
	return output.Result(result, "search: %d included, %d omitted", result.Included, result.Omitted)
}

func (core *MemoryCore) get(ctx context.Context, flags map[string]string, positional []string, output Output) error {
	memoryID := flags["memory"]
	if memoryID == "" && len(positional) > 0 {
		memoryID = positional[0]
	}
	if memoryID == "" || flags["workspace"] == "" {
		return &UsageError{Message: "memory get requires a memory ID and --workspace"}
	}
	session := flags["session"]
	if session == "" {
		session, _ = randomIdempotencyKey()
	}
	item, err := core.Client.Get(ctx, memoryID, flags["workspace"], session, ports.FieldCLIRead)
	if err != nil {
		return err
	}
	if err := core.guard(ctx, item.WorkspaceID, item.MemoryID, item.RevisionID, item.Title, item.Content, item.Tags); err != nil {
		return err
	}
	return output.Result(item, "%s\n%s", item.Title, item.Content)
}

func (core *MemoryCore) explain(ctx context.Context, flags map[string]string, positional []string, output Output) error {
	memoryID := flags["memory"]
	if memoryID == "" && len(positional) > 0 {
		memoryID = positional[0]
	}
	if memoryID == "" {
		return &UsageError{Message: "memory explain requires a memory ID"}
	}
	result, err := core.Client.Explain(ctx, memoryID)
	if err != nil {
		return err
	}
	return output.Result(result, "memory %s revision %s trust=%s lifecycle=%s", result.MemoryID, result.RevisionID, result.Trust, result.Lifecycle)
}

func (core *MemoryCore) guard(ctx context.Context, workspaceID, memoryID, revisionID, title, content string, tags []string) error {
	if core.Guard == nil {
		return nil
	}
	fields := []ports.TextField{{Name: ports.FieldCLIRead, Value: title}, {Name: ports.FieldCLIRead, Value: content}}
	for _, tag := range tags {
		fields = append(fields, ports.TextField{Name: ports.FieldCLIRead, Value: tag})
	}
	result, err := core.Guard.Check(ctx, application.OutputRequest{Route: ports.FieldCLIRead, WorkspaceID: workspaceID, MemoryID: memoryID, RevisionID: revisionID, Fields: fields})
	if err != nil {
		return err
	}
	if !result.Allowed {
		return domain.NewError(domain.CodeQuarantine, "content quarantined", false)
	}
	return nil
}

func (core *MemoryCore) idempotency(flags map[string]string) (string, error) {
	if value := flags["idempotency-key"]; value != "" {
		return value, nil
	}
	if core.Key == nil {
		return randomIdempotencyKey()
	}
	return core.Key()
}

func parseFlags(args []string) (map[string]string, []string, error) {
	flags := make(map[string]string)
	positional := make([]string, 0)
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if !strings.HasPrefix(arg, "--") {
			positional = append(positional, arg)
			continue
		}
		name := strings.TrimPrefix(arg, "--")
		if name == "" {
			return nil, nil, &UsageError{Message: "invalid empty flag"}
		}
		if name == "verified" || name == "global" || name == "pinned" || name == "apply" || name == "dry-run" {
			flags[name] = "true"
			continue
		}
		if equal := strings.IndexByte(name, '='); equal >= 0 {
			flags[name[:equal]] = name[equal+1:]
			continue
		}
		if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
			return nil, nil, &UsageError{Message: "flag value is required"}
		}
		index++
		flags[name] = args[index]
	}
	return flags, positional, nil
}

func splitTags(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}
func boolFlag(flags map[string]string, name string) bool {
	return flags[name] == "true" || flags[name] == "1"
}
func integerFlag(flags map[string]string, name string) (int, error) {
	if flags[name] == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(flags[name])
	if err != nil || value < 0 {
		return 0, &UsageError{Message: "invalid integer flag"}
	}
	return value, nil
}
func randomIdempotencyKey() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
