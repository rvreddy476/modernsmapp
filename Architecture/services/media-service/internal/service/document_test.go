package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Private documents (document.go): every guard of the kind, without a
// database, an object store or Redis.

var (
	docPDF  = []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n%%EOF\n")
	docJPEG = append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}, bytes.Repeat([]byte{0x11}, 40)...)
	docPNG  = append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n', 0, 0, 0, 13, 'I', 'H', 'D', 'R'}, bytes.Repeat([]byte{0x22}, 40)...)
)

// ── the type allow-list and the size cap, at init ────────────────────────

func TestDocumentUploadAcceptsExactlyPDFJPEGAndPNG(t *testing.T) {
	for _, mime := range []string{"application/pdf", "image/jpeg", "image/png", "Application/PDF", "application/pdf; charset=binary"} {
		if err := ValidateUploadMIME(mime, FileTypeDocument); err != nil {
			t.Errorf("ValidateUploadMIME(%q, document) = %v, want accepted", mime, err)
		}
		if err := ValidateUpload(FileTypeDocument, "general", mime, 1024); err != nil {
			t.Errorf("ValidateUpload(document, %q) = %v, want accepted", mime, err)
		}
	}
	for _, mime := range []string{
		"image/webp", "image/gif", "image/heic", "image/svg+xml", "text/html", "text/plain",
		"application/octet-stream", "application/zip", "application/x-pdf", "video/mp4", "",
	} {
		err := ValidateUploadMIME(mime, FileTypeDocument)
		if err == nil || !strings.Contains(err.Error(), "invalid document type") {
			t.Errorf("ValidateUploadMIME(%q, document) = %v, want an invalid document type", mime, err)
		}
		if err := ValidateUpload(FileTypeDocument, "general", mime, 1024); err == nil {
			t.Errorf("ValidateUpload(document, %q) accepted a type outside the allow-list", mime)
		}
	}
}

func TestDocumentUploadSizeCap(t *testing.T) {
	if err := ValidateUpload(FileTypeDocument, "general", "application/pdf", MaxDocumentSizeBytes); err != nil {
		t.Fatalf("a document of exactly the cap was refused: %v", err)
	}
	err := ValidateUpload(FileTypeDocument, "general", "application/pdf", MaxDocumentSizeBytes+1)
	if err == nil || !strings.Contains(err.Error(), "file too large") {
		t.Fatalf("a document one byte over the cap: %v, want a file too large refusal (413)", err)
	}
	if MaxDocumentSizeBytes != 10<<20 {
		t.Fatalf("MaxDocumentSizeBytes = %d, want 10 MiB", MaxDocumentSizeBytes)
	}
}

// The subtype switch in ValidateUpload returns early for avatar, cover and
// gif, asking only for an image/ prefix. A document must never reach it.
func TestDocumentUploadCannotBorrowAnImageSubtype(t *testing.T) {
	for _, sub := range []string{"avatar", "cover", "gif", "post_image"} {
		if err := ValidateUpload(FileTypeDocument, sub, "image/png", 1024); err == nil {
			t.Errorf("a document with media_subtype %q was accepted", sub)
		}
	}
	// And an oversize PNG document is refused even with the avatar subtype.
	if err := ValidateUpload(FileTypeDocument, "avatar", "image/png", MaxDocumentSizeBytes+1); err == nil {
		t.Error("an oversize document passed under the avatar subtype")
	}
}

// ── the magic bytes, at confirm ──────────────────────────────────────────

func TestDocumentMagicBytesMustBeTheDeclaredType(t *testing.T) {
	cases := []struct {
		name     string
		declared string
		head     []byte
		want     bool
	}{
		{"pdf", "application/pdf", docPDF, true},
		{"jpeg", "image/jpeg", docJPEG, true},
		{"png", "image/png", docPNG, true},
		{"pdf declared with params", "application/pdf; charset=binary", docPDF, true},
		{"png declared as pdf", "application/pdf", docPNG, false},
		{"pdf declared as jpeg", "image/jpeg", docPDF, false},
		{"jpeg declared as png", "image/png", docJPEG, false},
		{"html as pdf", "application/pdf", []byte("<html><script>alert(1)</script></html>"), false},
		{"pdf marker not at offset 0", "application/pdf", append([]byte(" "), docPDF...), false},
		{"pdf without the dash", "application/pdf", []byte("%PDF1.7 and more bytes here"), false},
		{"two jpeg bytes only", "image/jpeg", []byte{0xFF, 0xD8}, false},
		{"png signature cut short", "image/png", docPNG[:7], false},
		{"webp is not a document", "image/webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), false},
		{"empty", "application/pdf", nil, false},
	}
	for _, c := range cases {
		if got := DocumentMagicMatches(c.declared, c.head); got != c.want {
			t.Errorf("%s: DocumentMagicMatches = %v, want %v", c.name, got, c.want)
		}
		// The confirm path asks through uploadHeaderValid.
		if got := uploadHeaderValid(FileTypeDocument, c.declared, c.head); got != c.want {
			t.Errorf("%s: uploadHeaderValid(document) = %v, want %v", c.name, got, c.want)
		}
	}
}

// ── a separate upload budget ─────────────────────────────────────────────

var errLimited = errors.New("upload rate limit exceeded: test")

// InitUpload stops at the limiter here (the stores are nil): which limiter
// it asked is the whole assertion.
func TestDocumentInitSpendsTheDocumentBudgetOnly(t *testing.T) {
	var media, docs int
	s := &Service{
		uploadLimit:         func(context.Context, uuid.UUID) error { media++; return nil },
		documentUploadLimit: func(context.Context, uuid.UUID) error { docs++; return errLimited },
	}
	_, err := s.InitUpload(context.Background(), uuid.New(), FileTypeDocument, "", "application/pdf", 2048, "", false, "")
	if !errors.Is(err, errLimited) {
		t.Fatalf("err = %v, want the document limiter's refusal", err)
	}
	if docs != 1 || media != 0 {
		t.Fatalf("document init charged document=%d media=%d, want 1 and 0", docs, media)
	}
}

func TestImageInitNeverSpendsTheDocumentBudget(t *testing.T) {
	var media, docs int
	s := &Service{
		uploadLimit:         func(context.Context, uuid.UUID) error { media++; return errLimited },
		documentUploadLimit: func(context.Context, uuid.UUID) error { docs++; return nil },
	}
	_, err := s.InitUpload(context.Background(), uuid.New(), "image", "general", "image/jpeg", 2048, "", false, "")
	if !errors.Is(err, errLimited) {
		t.Fatalf("err = %v, want the media limiter's refusal", err)
	}
	if media != 1 || docs != 0 {
		t.Fatalf("image init charged media=%d document=%d, want 1 and 0", media, docs)
	}
}

// A document the validator refuses spends nothing.
func TestRefusedDocumentSpendsNoBudget(t *testing.T) {
	calls := 0
	s := &Service{
		uploadLimit:         func(context.Context, uuid.UUID) error { calls++; return nil },
		documentUploadLimit: func(context.Context, uuid.UUID) error { calls++; return nil },
	}
	for _, c := range []struct {
		sub, mime string
		size      int64
	}{
		{"general", "text/html", 100},
		{"general", "application/pdf", MaxDocumentSizeBytes + 1},
		{"avatar", "image/png", 100},
	} {
		if _, err := s.InitUpload(context.Background(), uuid.New(), FileTypeDocument, c.sub, c.mime, c.size, "", false, ""); err == nil {
			t.Fatalf("%+v was accepted", c)
		}
	}
	if calls != 0 {
		t.Fatalf("a refused document was charged %d time(s)", calls)
	}
}

func TestDocumentBudgetIsItsOwnCounter(t *testing.T) {
	user := uuid.New()
	key := documentDayKey(user)
	for _, media := range []string{"upload:rate:" + user.String() + ":hour", "upload:rate:" + user.String() + ":day"} {
		if key == media {
			t.Fatalf("the document counter %q is the media counter", key)
		}
	}
	if !strings.Contains(key, user.String()) {
		t.Fatalf("the document counter %q is not per user", key)
	}
	if MaxDocumentUploadsPerDay != 20 {
		t.Fatalf("MaxDocumentUploadsPerDay = %d, want 20", MaxDocumentUploadsPerDay)
	}
}

func TestResumableRefusesADocument(t *testing.T) {
	calls := 0
	s := &Service{
		uploadLimit:         func(context.Context, uuid.UUID) error { calls++; return nil },
		documentUploadLimit: func(context.Context, uuid.UUID) error { calls++; return nil },
	}
	if _, err := s.InitResumableUpload(context.Background(), uuid.New(), FileTypeDocument, "application/pdf", 1024); err == nil {
		t.Fatal("a document opened a resumable upload")
	}
	if calls != 0 {
		t.Fatalf("a refused resumable document was charged %d time(s)", calls)
	}
}

// ── no viewer read serves a document ─────────────────────────────────────

func TestViewerReadsRefuseADocumentToEveryone(t *testing.T) {
	owner := uuid.New()
	doc := &postgres.MediaAsset{ID: uuid.New(), UploaderID: owner, FileType: FileTypeDocument,
		ProcessingStatus: "ready", ModerationStatus: "passed"}
	image := &postgres.MediaAsset{ID: uuid.New(), UploaderID: owner, FileType: "image",
		ProcessingStatus: "ready", ModerationStatus: "passed"}
	for _, viewer := range []uuid.UUID{owner, uuid.New(), uuid.Nil} {
		if !ViewerReadDenies(doc, viewer) {
			t.Errorf("a document was not refused to viewer %s", viewer)
		}
		if ViewerReadDenies(image, viewer) {
			t.Errorf("a plain image was refused to viewer %s", viewer)
		}
		if captionReadLocalVerdict(doc, viewer) != captionReadDenied {
			t.Errorf("the caption/anonymous-stream verdict for a document is not denied (viewer %s)", viewer)
		}
		if !downloadScopeDenies(doc, viewer) {
			t.Errorf("a document is downloadable by viewer %s", viewer)
		}
	}
	// The dating rule is still in it.
	dating := &postgres.MediaAsset{ID: uuid.New(), UploaderID: owner, FileType: "image", AccessScope: postgres.AccessScopeDatingPhoto}
	if !ViewerReadDenies(dating, uuid.New()) || ViewerReadDenies(dating, owner) {
		t.Error("ViewerReadDenies lost the dating rule")
	}
}

// allowAll is a content authority that admits everyone: the document rule is
// then the only thing that can refuse.
type allowAll struct{}

func (allowAll) Authorize(context.Context, string, string) error { return nil }

func TestDocumentRecordIsTheUploadersAlone(t *testing.T) {
	owner := uuid.New()
	doc := &postgres.MediaAsset{ID: uuid.New(), UploaderID: owner, FileType: FileTypeDocument,
		StorageKey: "user/" + owner.String() + "/x/original", ProcessingStatus: "ready", ModerationStatus: "passed"}
	gate := delivery.NewGate(noopSigner{}, allowAll{})
	if err := authorizeMediaRecord(context.Background(), gate, doc, owner); err != nil {
		t.Fatalf("the uploader cannot read their own document's record: %v", err)
	}
	for _, viewer := range []uuid.UUID{uuid.New(), uuid.Nil} {
		if err := authorizeMediaRecord(context.Background(), gate, doc, viewer); !errors.Is(err, delivery.ErrDeliveryDenied) {
			t.Errorf("viewer %s read a document's record (err %v) behind an authority that admits everyone", viewer, err)
		}
	}
}

// ── the document-bytes reader ────────────────────────────────────────────

type docRecords map[uuid.UUID]*postgres.MediaAsset

func (r docRecords) GetMedia(_ context.Context, id uuid.UUID) (*postgres.MediaAsset, error) {
	m, ok := r[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	cp := *m
	return &cp, nil
}

type docObject struct {
	data []byte
	etag string
	size int64 // reported size; -1 means len(data)
}

type docObjects struct {
	objects map[string]docObject
	broken  map[string]bool
}

func (o docObjects) OpenObject(_ context.Context, key string) (io.ReadCloser, blob.ObjectInfo, error) {
	if o.broken[key] {
		return nil, blob.ObjectInfo{}, errors.New("blob: connection reset")
	}
	obj, ok := o.objects[key]
	if !ok {
		return nil, blob.ObjectInfo{}, blob.ErrObjectNotFound
	}
	size := obj.size
	if size < 0 {
		size = int64(len(obj.data))
	}
	return io.NopCloser(bytes.NewReader(obj.data)), blob.ObjectInfo{Size: size, ETag: obj.etag}, nil
}

type docFixture struct {
	records docRecords
	objects docObjects
	reader  *DocumentReader
}

// add stores a ready, confirmed document whose object is exactly what was
// confirmed; edit may then break one property.
func (f *docFixture) add(mime string, body []byte, edit func(m *postgres.MediaAsset, o *docObject)) uuid.UUID {
	id, owner := uuid.New(), uuid.New()
	m := &postgres.MediaAsset{
		ID: id, UploaderID: owner, FileType: FileTypeDocument, MimeType: mime,
		FileSizeBytes: int64(len(body)), StorageKey: "user/" + owner.String() + "/" + id.String() + "/original",
		ProcessingStatus: "ready", ModerationStatus: "passed", OriginalETag: "etag-" + id.String(),
	}
	o := docObject{data: body, etag: m.OriginalETag, size: -1}
	if edit != nil {
		edit(m, &o)
	}
	f.records[id] = m
	f.objects.objects[m.StorageKey] = o
	return id
}

func newDocFixture() *docFixture {
	f := &docFixture{records: docRecords{}, objects: docObjects{objects: map[string]docObject{}, broken: map[string]bool{}}}
	f.reader = NewDocumentReader(f.records, f.objects)
	return f
}

func TestDocumentReaderServesTheVerifiedBytesUnchanged(t *testing.T) {
	f := newDocFixture()
	for _, c := range []struct {
		mime string
		body []byte
	}{{"application/pdf", docPDF}, {"image/jpeg", docJPEG}, {"image/png", docPNG}} {
		id := f.add(c.mime, c.body, nil)
		got, err := f.reader.DocumentBytes(context.Background(), id)
		if err != nil {
			t.Fatalf("%s: %v", c.mime, err)
		}
		if got.ContentType != c.mime || !bytes.Equal(got.Bytes, c.body) {
			t.Fatalf("%s: served %q (%d bytes), want the stored bytes as %s", c.mime, got.ContentType, len(got.Bytes), c.mime)
		}
	}
}

func TestDocumentReaderRefusals(t *testing.T) {
	f := newDocFixture()
	html := []byte("<html><script>x</script>" + strings.Repeat(" ", len(docPDF)-24) + "</html>")[:len(docPDF)]
	cases := map[string]uuid.UUID{
		"an image row":           f.add("application/pdf", docPDF, func(m *postgres.MediaAsset, _ *docObject) { m.FileType = "image" }),
		"a video row":            f.add("application/pdf", docPDF, func(m *postgres.MediaAsset, _ *docObject) { m.FileType = "video" }),
		"not ready":              f.add("application/pdf", docPDF, func(m *postgres.MediaAsset, _ *docObject) { m.ProcessingStatus = "uploaded" }),
		"rejected":               f.add("application/pdf", docPDF, func(m *postgres.MediaAsset, _ *docObject) { m.ModerationStatus = "rejected" }),
		"a public key":           f.add("application/pdf", docPDF, func(m *postgres.MediaAsset, _ *docObject) { m.StorageKey = "public/" + m.StorageKey }),
		"no pinned etag":         f.add("application/pdf", docPDF, func(m *postgres.MediaAsset, _ *docObject) { m.OriginalETag = "" }),
		"replaced after confirm": f.add("application/pdf", docPDF, func(_ *postgres.MediaAsset, o *docObject) { o.etag = "another" }),
		"size differs":           f.add("application/pdf", docPDF, func(_ *postgres.MediaAsset, o *docObject) { o.data = append(o.data, 'x'); o.size = -1 }),
		"store reports oversize": f.add("application/pdf", docPDF, func(_ *postgres.MediaAsset, o *docObject) { o.size = MaxDocumentSizeBytes + 1 }),
		"row oversize": f.add("application/pdf", docPDF, func(m *postgres.MediaAsset, o *docObject) {
			big := append(append([]byte{}, docPDF...), bytes.Repeat([]byte{'a'}, int(MaxDocumentSizeBytes))...)
			o.data, o.size, m.FileSizeBytes = big, -1, int64(len(big))
		}),
		"bytes no longer a pdf": f.add("application/pdf", docPDF, func(_ *postgres.MediaAsset, o *docObject) { o.data = html }),
		"png stored as pdf":     f.add("application/pdf", docPNG, nil),
		"object gone": f.add("application/pdf", docPDF, func(m *postgres.MediaAsset, _ *docObject) {
			m.StorageKey = "user/" + m.UploaderID.String() + "/" + m.ID.String() + "/original"
		}),
		"no such row": uuid.New(),
	}
	gone := cases["object gone"]
	delete(f.objects.objects, f.records[gone].StorageKey)
	for name, id := range cases {
		got, err := f.reader.DocumentBytes(context.Background(), id)
		if !errors.Is(err, ErrDocumentNotFound) {
			t.Errorf("%s: (%v, %v), want ErrDocumentNotFound", name, got, err)
		}
	}
}

func TestDocumentReaderOutageIsNotNotFound(t *testing.T) {
	f := newDocFixture()
	id := f.add("application/pdf", docPDF, nil)
	f.objects.broken[f.records[id].StorageKey] = true
	_, err := f.reader.DocumentBytes(context.Background(), id)
	if err == nil || errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("an object store outage answered %v; want an error that is not not-found (503, retry)", err)
	}
}
