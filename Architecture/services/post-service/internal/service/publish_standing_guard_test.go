package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The publication guard (Copyright Match plan 6.4, P-5; T4-5).
//
// # WHY A GUARD AND NOT A LIST IN A COMMENT
//
// The defect this closes was a MISSING check: standing was enforced on one
// of thirteen publication paths, and nobody noticed for months because
// nothing compared the list of paths with the list of checks. So this test
// derives both from the source. It fails when
//
//   - a named publication entry point stops calling the choke point (or
//     the delegate that calls it);
//   - any function in this package reaches a store sink that makes content
//     live without being a named entry point;
//   - a new writer of posts / stories / reposts appears in the store
//     package without being named as a sink here.
//
// Adding a publication path means adding it here, with its gate.

// gate says how an entry point is protected.
type gate string

const (
	// gateInteractive: calls requirePublishStanding itself.
	gateInteractive gate = "interactive"
	// gateBackground: calls backgroundPublishStanding itself.
	gateBackground gate = "background"
	// gateBoth: calls both (one path serves the worker and the author).
	gateBoth gate = "both"
)

type publicationEntry struct {
	file string
	gate gate
	// via names the function this entry delegates the check to; that
	// function must itself be a named entry.
	via string
}

// publicationEntryPoints is the inventory: every function in this package
// that makes content live on an author's behalf. The plan's thirteen call
// sites are all here (post.go CreatePost / PublishVideo / CreateRepost;
// drafts.go PublishDraft / PublishScheduledDrafts; schedule.go
// PublishScheduled / ReschedulePost; postschedule/worker.go via
// PublishScheduled; threads.go; live_vod.go; story_surface.go;
// crosspost.go; post_drafts.go).
var publicationEntryPoints = map[string]publicationEntry{
	"CreatePost":         {file: "post.go", gate: gateInteractive},
	"CreateThread":       {file: "threads.go", gate: gateInteractive},
	"CreateRepost":       {file: "post.go", gate: gateInteractive},
	"CreateCrosspost":    {file: "crosspost.go", gate: gateInteractive},
	"CreateStoryPending": {file: "story_surface.go", gate: gateInteractive},
	"CreateLiveVODPost":  {file: "live_vod.go", gate: gateBackground},

	"PublishDraft":            {file: "drafts.go", gate: gateInteractive},
	"publishClaimedReelDraft": {file: "drafts.go", gate: gateBackground},
	"PublishScheduledDrafts":  {file: "drafts.go", via: "publishClaimedReelDraft"},

	"PublishPostDraft":           {file: "post_drafts.go", via: "publishDraftRow"},
	"publishDraftRow":            {file: "post_drafts.go", via: "draftPublishStanding"},
	"draftPublishStanding":       {file: "post_drafts.go", gate: gateBoth},
	"PublishScheduledPostDrafts": {file: "post_drafts.go", via: "publishDraftRow"},

	"PublishScheduled": {file: "schedule.go", gate: gateBoth},
	"ReschedulePost":   {file: "schedule.go", via: "PublishScheduled"},
	"PublishVideo":     {file: "post.go", via: "PublishScheduled"},
}

// serviceSinks are the calls in THIS package that make content live. Any
// function that contains one must be a named entry point.
var serviceSinks = []string{
	"pgStore.CreatePost",
	"pgStore.CreateThread",
	"pgStore.PublishScheduledPost",
	"pgStore.CreateCrosspostLink",
	"pgStore.CreateStoryPending",
	"pgStore.CreateStory",
	"pgStore.CreateRepost",
	"pgStore.SwitchRepostType",
	"liveVOD.CreateLiveVODPost",
	"s.CreatePost", // the service's own CreatePost is a sink for its wrappers
}

// storeSinks are the functions in internal/store/postgres allowed to
// INSERT INTO posts / stories / reposts. A new writer fails the test until
// it is listed here AND reached only through a named entry point above.
var storeSinks = map[string]string{
	"insertPostTx":        "posts.go: the one INSERT INTO posts, called by CreatePost, CreateThread and CreateLiveVODPost",
	"CreateCrosspostLink": "crosspost_links.go: the embed post",
	"CreateStory":         "stories.go: legacy story insert (CreateStory, superseded by CreateStoryPending; no service caller)",
	"CreateStoryPending":  "story_create.go: the pending story",
	"CreateRepost":        "reposts.go",
	"SwitchRepostType":    "reposts.go",
}

const (
	helperInteractive = "requirePublishStanding"
	helperBackground  = "backgroundPublishStanding"
)

// parsePackage parses every non-test .go file in dir.
func parsePackage(t *testing.T, dir string) map[string]*ast.File {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*ast.File{}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		out[filepath.Base(f)] = af
	}
	return out
}

// calledNames collects "recv.Sel" and "Sel" for every call in body.
func calledNames(body *ast.BlockStmt) map[string]bool {
	out := map[string]bool{}
	if body == nil {
		return out
	}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			out[fn.Sel.Name] = true
			if x, ok := fn.X.(*ast.Ident); ok {
				out[x.Name+"."+fn.Sel.Name] = true
			}
			if inner, ok := fn.X.(*ast.SelectorExpr); ok {
				out[inner.Sel.Name+"."+fn.Sel.Name] = true
			}
		case *ast.Ident:
			out[fn.Name] = true
		}
		return true
	})
	return out
}

// serviceFuncs indexes every *Service method and top-level func by name.
func serviceFuncs(files map[string]*ast.File) map[string]struct {
	file string
	decl *ast.FuncDecl
} {
	out := map[string]struct {
		file string
		decl *ast.FuncDecl
	}{}
	for name, af := range files {
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			out[fd.Name.Name] = struct {
				file string
				decl *ast.FuncDecl
			}{name, fd}
		}
	}
	return out
}

func TestEveryPublicationEntryPointChecksStanding(t *testing.T) {
	funcs := serviceFuncs(parsePackage(t, "."))
	for name, entry := range publicationEntryPoints {
		fn, ok := funcs[name]
		if !ok {
			t.Errorf("%s: named in publicationEntryPoints but not found in this package (renamed? update the inventory)", name)
			continue
		}
		if fn.file != entry.file {
			t.Errorf("%s: inventory says %s, found in %s", name, entry.file, fn.file)
		}
		calls := calledNames(fn.decl.Body)
		switch {
		case entry.via != "":
			if _, known := publicationEntryPoints[entry.via]; !known {
				t.Errorf("%s: delegates to %s, which is not a named entry point", name, entry.via)
			}
			if !calls[entry.via] {
				t.Errorf("%s (%s): must publish through %s, which carries the standing check; it does not call it", name, fn.file, entry.via)
			}
		case entry.gate == gateInteractive:
			if !calls[helperInteractive] {
				t.Errorf("%s (%s): interactive publication path does not call %s", name, fn.file, helperInteractive)
			}
		case entry.gate == gateBackground:
			if !calls[helperBackground] {
				t.Errorf("%s (%s): background publication path does not call %s", name, fn.file, helperBackground)
			}
		case entry.gate == gateBoth:
			if !calls[helperInteractive] || !calls[helperBackground] {
				t.Errorf("%s (%s): serves the author and the worker; must call both %s and %s", name, fn.file, helperInteractive, helperBackground)
			}
		default:
			t.Errorf("%s: no gate and no via", name)
		}
	}
}

func TestNoUnlistedFunctionReachesAPublicationSink(t *testing.T) {
	funcs := serviceFuncs(parsePackage(t, "."))
	checked := 0
	for name, fn := range funcs {
		calls := calledNames(fn.decl.Body)
		for _, sink := range serviceSinks {
			if !calls[sink] {
				continue
			}
			checked++
			if _, listed := publicationEntryPoints[name]; !listed {
				t.Errorf("%s (%s) reaches the publication sink %s but is not in publicationEntryPoints.\n"+
					"  Every path that makes content live must call requirePublishStanding (or\n"+
					"  backgroundPublishStanding) and be listed with its gate.", name, fn.file, sink)
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d sink references found; the sink patterns no longer match the code", checked)
	}
}

var (
	storeFuncDecl = regexp.MustCompile(`^func (?:\([^)]*\) )?([A-Za-z0-9_]+)\(`)
	storeInsert   = regexp.MustCompile(`INSERT INTO (posts|stories|reposts)\b`)
)

func TestEveryStorePostWriterIsAKnownSink(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "store", "postgres", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		current := ""
		for i, line := range strings.Split(string(src), "\n") {
			if m := storeFuncDecl.FindStringSubmatch(line); m != nil {
				current = m[1]
			}
			if !storeInsert.MatchString(line) {
				continue
			}
			found++
			if _, ok := storeSinks[current]; !ok {
				t.Errorf("%s:%d: %s writes %s but is not a known publication sink.\n"+
					"  Add it to storeSinks and make sure its only service callers are named\n"+
					"  publication entry points (TestEveryPublicationEntryPointChecksStanding).",
					filepath.Base(file), i+1, current, strings.TrimSpace(line))
			}
		}
	}
	if found < 4 {
		t.Fatalf("found %d INSERTs; the pattern no longer matches the store", found)
	}
}

// The worker in internal/postschedule publishes only through the service's
// PublishScheduled, which carries the check. Pin that so a future worker
// cannot flip rows itself.
func TestScheduleWorkerPublishesThroughTheService(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "postschedule", "worker.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "publisher.PublishScheduled(") {
		t.Fatal("postschedule.Worker.Tick must publish through Publisher.PublishScheduled")
	}
	if strings.Contains(string(src), "INSERT") || strings.Contains(string(src), "UPDATE posts") {
		t.Fatal("the schedule worker must not write posts itself")
	}
}
