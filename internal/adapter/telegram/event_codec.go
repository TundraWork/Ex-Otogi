package telegram

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

const (
	maxEncodedEventBytes           = 2 * 1024 * 1024
	maxCodecCollectionCount        = 1024
	codecVersion            uint16 = 1
)

var codecMagic = [4]byte{'O', 'T', 'G', 'E'}

type eventWriter struct {
	bytes.Buffer
	err error
}

func (w *eventWriter) u8(v byte) {
	if w.err == nil {
		w.err = w.WriteByte(v)
	}
}
func (w *eventWriter) u32(v uint32) {
	if w.err == nil {
		w.err = binary.Write(&w.Buffer, binary.BigEndian, v)
	}
}
func (w *eventWriter) i64(v int64) {
	if w.err == nil {
		w.err = binary.Write(&w.Buffer, binary.BigEndian, v)
	}
}
func (w *eventWriter) f64(v float64) { w.i64(int64(math.Float64bits(v))) }
func (w *eventWriter) str(v string)  { w.blob([]byte(v)) }
func (w *eventWriter) blob(v []byte) {
	if len(v) > math.MaxUint32 {
		w.err = fmt.Errorf("telegram event codec: field too large")
		return
	}
	w.u32(uint32(len(v)))
	if w.err == nil {
		_, w.err = w.Write(v)
	}
}
func (w *eventWriter) count(n int) {
	if n > maxCodecCollectionCount {
		w.err = fmt.Errorf("telegram event codec: excessive collection")
		return
	}
	w.u32(uint32(n))
}

type eventReader struct {
	ctx  context.Context
	data []byte
	off  int
}

func (r *eventReader) take(n int) ([]byte, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	if n < 0 || n > len(r.data)-r.off {
		return nil, fmt.Errorf("telegram event codec: malformed payload")
	}
	b := r.data[r.off : r.off+n]
	r.off += n
	return b, nil
}
func (r *eventReader) u8() (byte, error) {
	b, e := r.take(1)
	if e != nil {
		return 0, e
	}
	return b[0], nil
}
func (r *eventReader) u32() (uint32, error) {
	b, e := r.take(4)
	if e != nil {
		return 0, e
	}
	return binary.BigEndian.Uint32(b), nil
}
func (r *eventReader) i64() (int64, error) {
	b, e := r.take(8)
	if e != nil {
		return 0, e
	}
	return int64(binary.BigEndian.Uint64(b)), nil
}
func (r *eventReader) f64() (float64, error) {
	v, e := r.i64()
	return math.Float64frombits(uint64(v)), e
}
func (r *eventReader) blob(max int) ([]byte, error) {
	n, e := r.u32()
	if e != nil {
		return nil, e
	}
	if n > uint32(max) {
		return nil, fmt.Errorf("telegram event codec: field bound exceeded")
	}
	return r.take(int(n))
}
func (r *eventReader) str(max int) (string, error) { b, e := r.blob(max); return string(b), e }
func (r *eventReader) count(max int) (int, error) {
	n, e := r.u32()
	if e != nil {
		return 0, e
	}
	if n > uint32(max) || n > maxCodecCollectionCount {
		return 0, fmt.Errorf("telegram event codec: collection bound exceeded")
	}
	return int(n), nil
}

// EncodeEventInput serializes an immutable event into the Telegram durable-inbox wire format.
func EncodeEventInput(v otogi.EventInput) (encoded []byte, err error) {
	defer func() { err = classifyCodecError(err) }()
	if v.Platform().String() == "" || v.Account().String() == "" || v.Tenant().String() == "" || v.Conversation().String() == "" || v.OccurredAt().IsZero() || v.IngestedAt().IsZero() {
		return nil, fmt.Errorf("telegram event codec: invalid event input")
	}
	w := &eventWriter{}
	w.Write(codecMagic[:])
	_ = binary.Write(&w.Buffer, binary.BigEndian, codecVersion)
	w.str(v.Platform().String())
	w.str(v.Account().String())
	w.str(v.Tenant().String())
	w.str(v.Conversation().String())
	w.str(optionalSource(v))
	w.str(optionalActor(v))
	w.str(v.Action().String())
	w.str(string(v.Direction()))
	w.str(v.OccurredAt().Format(time.RFC3339Nano))
	w.str(v.IngestedAt().Format(time.RFC3339Nano))
	w.str(optionalTime(v))
	w.str(optionalTrace(v))
	w.str(optionalCausal(v))
	w.str(string(v.Completeness()))
	writeLocator(w, optionalLocator(v))
	writeMessage(w, v.Message())
	writeExtensions(w, v.Extensions())
	if w.err != nil {
		return nil, w.err
	}
	if w.Len() > maxEncodedEventBytes {
		return nil, fmt.Errorf("telegram event codec: payload exceeds bound")
	}
	return w.Bytes(), nil
}

func classifyCodecError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, otogi.ErrInvalid) {
		return err
	}
	return fmt.Errorf("%w: %v", otogi.ErrInvalid, err)
}

func optionalSource(v otogi.EventInput) string {
	x, ok := v.SourceEventID()
	if ok {
		return x.String()
	}
	return ""
}
func optionalActor(v otogi.EventInput) string {
	x, ok := v.Actor()
	if ok {
		return x.String()
	}
	return ""
}
func optionalTrace(v otogi.EventInput) string {
	x, ok := v.TraceID()
	if ok {
		return x.String()
	}
	return ""
}
func optionalCausal(v otogi.EventInput) string {
	x, ok := v.CausalEventID()
	if ok {
		return x.String()
	}
	return ""
}
func optionalTime(v otogi.EventInput) string {
	x, ok := v.EditedAt()
	if ok {
		return x.Format(time.RFC3339Nano)
	}
	return ""
}
func optionalLocator(v otogi.EventInput) otogi.ProviderLocator { x, _ := v.ProviderLocator(); return x }

func writeExtensions(w *eventWriter, xs []otogi.Extension) {
	w.count(len(xs))
	for _, x := range xs {
		w.str(x.NamespaceURI())
		w.str(x.LocalName())
		w.u32(x.SchemaVersion())
		w.str(x.MediaType())
		w.blob(x.Data())
	}
}
func writeLocator(w *eventWriter, x otogi.ProviderLocator) {
	if x.NamespaceURI() == "" {
		w.u8(0)
		return
	}
	w.u8(1)
	w.str(x.NamespaceURI())
	w.u32(x.SchemaVersion())
	w.blob(x.Data())
}
func writeIDs(w *eventWriter, xs []otogi.PartID) {
	w.count(len(xs))
	for _, x := range xs {
		w.str(x.String())
	}
}
func writeMessage(w *eventWriter, m otogi.Message) {
	writeIDs(w, m.Roots())
	parts := m.Parts()
	w.count(len(parts))
	for _, p := range parts {
		writePart(w, p)
	}
	rels := m.Relations()
	w.count(len(rels))
	for _, x := range rels {
		writeRelation(w, x)
	}
	writeExtensions(w, m.Extensions())
}
func writePart(w *eventWriter, p otogi.Part) {
	w.str(p.ID().String())
	w.str(string(p.Kind()))
	writeIDs(w, p.Children())
	writeExtensions(w, p.Extensions())
	switch p.Kind() {
	case otogi.PartText:
		s, a, _ := p.Text()
		w.str(s)
		w.count(len(a))
		for _, x := range a {
			w.str(x.ID())
			w.i64(int64(x.Offset()))
			w.i64(int64(x.Length()))
			w.str(string(x.Kind()))
			w.str(x.Target())
			writeExtensions(w, x.Extensions())
		}
	case otogi.PartMedia, otogi.PartFile:
		m, _ := p.Media()
		writeMedia(w, m)
	case otogi.PartLocation:
		x, _ := p.Location()
		w.f64(x.Latitude())
		w.f64(x.Longitude())
		w.str(x.Label())
	case otogi.PartContact:
		x, _ := p.Contact()
		w.str(x.DisplayName())
		w.str(x.Address())
	case otogi.PartPoll:
		x, _ := p.Poll()
		w.str(x.Question())
		opts := x.Options()
		w.count(len(opts))
		for _, o := range opts {
			w.str(o)
		}
		if x.Multiple() {
			w.u8(1)
		} else {
			w.u8(0)
		}
	case otogi.PartInteractive:
		x, _ := p.Interactive()
		w.str(x.Layout())
		w.str(x.ActionID())
		w.str(x.Label())
	case otogi.PartEmbeddedMessage:
		x, _ := p.EmbeddedMessage()
		w.str(x.Summary())
		w.str(x.Reference())
	case otogi.PartOpaque:
		x, _ := p.Value()
		writeExtensions(w, []otogi.Extension{x})
	}
}
func writeMedia(w *eventWriter, m otogi.Media) {
	w.str(string(m.Role()))
	w.str(m.MediaType())
	w.str(m.Filename())
	w.i64(m.ByteSize())
	w.i64(int64(m.Width()))
	w.i64(int64(m.Height()))
	w.i64(int64(m.Duration()))
	hs := m.Hashes()
	w.count(len(hs))
	for _, h := range hs {
		w.str(h.Algorithm())
		w.blob(h.Value())
	}
	s := m.Source()
	w.str(string(s.Kind()))
	switch s.Kind() {
	case otogi.MediaSourceInline:
		b, _ := s.Inline()
		w.blob(b)
	case otogi.MediaSourceExternal:
		u, _ := s.ExternalURI()
		w.str(u)
	case otogi.MediaSourceOpaque:
		l, _ := s.OpaqueLocator()
		writeLocator(w, l)
	}
	writeExtensions(w, s.Extensions())
	writeExtensions(w, m.Extensions())
}
func writeRelation(w *eventWriter, x otogi.Relation) {
	w.str(string(x.Kind()))
	t := x.Target()
	w.str(t.Platform().String())
	w.str(t.Account().String())
	w.str(t.Conversation().String())
	w.str(t.SourceEvent().String())
	writeLocator(w, t.Locator())
	w.str(x.Key())
	writeExtensions(w, x.Extensions())
}

// DecodeEventInput reconstructs one strictly framed, bounded Telegram durable-inbox event.
func DecodeEventInput(ctx context.Context, data []byte) (decoded otogi.EventInput, err error) {
	defer func() { err = classifyCodecError(err) }()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return otogi.EventInput{}, err
	}
	if len(data) > maxEncodedEventBytes {
		return otogi.EventInput{}, fmt.Errorf("telegram event codec: payload exceeds bound")
	}
	r := &eventReader{ctx: ctx, data: data}
	magic, e := r.take(4)
	if e != nil || !bytes.Equal(magic, codecMagic[:]) {
		return otogi.EventInput{}, fmt.Errorf("telegram event codec: invalid magic")
	}
	vb, e := r.take(2)
	if e != nil {
		return otogi.EventInput{}, e
	}
	if binary.BigEndian.Uint16(vb) != codecVersion {
		return otogi.EventInput{}, fmt.Errorf("telegram event codec: unsupported version")
	}
	platform, e := readID(r, otogi.NewPlatformID)
	if e != nil {
		return otogi.EventInput{}, e
	}
	account, e := readID(r, otogi.NewAccountID)
	if e != nil {
		return otogi.EventInput{}, e
	}
	tenant, e := readID(r, otogi.NewTenantID)
	if e != nil {
		return otogi.EventInput{}, e
	}
	conversation, e := readID(r, otogi.NewConversationID)
	if e != nil {
		return otogi.EventInput{}, e
	}
	source, e := readOptionalID(r, otogi.NewSourceEventID)
	if e != nil {
		return otogi.EventInput{}, e
	}
	actor, e := readOptionalID(r, otogi.NewActorID)
	if e != nil {
		return otogi.EventInput{}, e
	}
	as, e := r.str(512)
	if e != nil {
		return otogi.EventInput{}, e
	}
	action, e := otogi.NewEventAction(as)
	if e != nil {
		return otogi.EventInput{}, e
	}
	ds, e := r.str(32)
	if e != nil {
		return otogi.EventInput{}, e
	}
	occurred, e := readTime(r, false)
	if e != nil {
		return otogi.EventInput{}, e
	}
	ingested, e := readTime(r, false)
	if e != nil {
		return otogi.EventInput{}, e
	}
	edited, e := readTime(r, true)
	if e != nil {
		return otogi.EventInput{}, e
	}
	trace, e := readOptionalID(r, otogi.NewTraceID)
	if e != nil {
		return otogi.EventInput{}, e
	}
	causal, e := readOptionalID(r, otogi.NewCausalEventID)
	if e != nil {
		return otogi.EventInput{}, e
	}
	cs, e := r.str(32)
	if e != nil {
		return otogi.EventInput{}, e
	}
	locator, e := readLocator(r)
	if e != nil {
		return otogi.EventInput{}, e
	}
	message, e := readMessage(r)
	if e != nil {
		return otogi.EventInput{}, e
	}
	exts, e := readExtensions(r, 64)
	if e != nil {
		return otogi.EventInput{}, e
	}
	if r.off != len(data) {
		return otogi.EventInput{}, fmt.Errorf("telegram event codec: trailing data")
	}
	return otogi.NewEventInput(otogi.EventInputParams{Platform: platform, Account: account, Tenant: tenant, Conversation: conversation, SourceEventID: source, Actor: actor, Action: action, Direction: otogi.Direction(ds), OccurredAt: occurred, IngestedAt: ingested, EditedAt: edited, TraceID: trace, CausalEventID: causal, Completeness: otogi.Completeness(cs), ProviderLocator: locator, Message: message, Extensions: exts})
}

func readID[T any](r *eventReader, newID func(string) (T, error)) (T, error) {
	var z T
	s, e := r.str(128)
	if e != nil {
		return z, e
	}
	return newID(s)
}
func readOptionalID[T any](r *eventReader, newID func(string) (T, error)) (T, error) {
	var z T
	s, e := r.str(128)
	if e != nil || s == "" {
		return z, e
	}
	return newID(s)
}
func readTime(r *eventReader, optional bool) (time.Time, error) {
	s, e := r.str(64)
	if e != nil {
		return time.Time{}, e
	}
	if optional && s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, s)
}
func readLocator(r *eventReader) (otogi.ProviderLocator, error) {
	flag, e := r.u8()
	if e != nil {
		return otogi.ProviderLocator{}, e
	}
	if flag == 0 {
		return otogi.ProviderLocator{}, nil
	}
	if flag != 1 {
		return otogi.ProviderLocator{}, fmt.Errorf("telegram event codec: invalid locator flag")
	}
	ns, e := r.str(4096)
	if e != nil {
		return otogi.ProviderLocator{}, e
	}
	v, e := r.u32()
	if e != nil {
		return otogi.ProviderLocator{}, e
	}
	data, e := r.blob(16 * 1024)
	if e != nil {
		return otogi.ProviderLocator{}, e
	}
	return otogi.NewProviderLocator(ns, v, data)
}
func readExtensions(r *eventReader, max int) ([]otogi.Extension, error) {
	n, e := r.count(max)
	if e != nil {
		return nil, e
	}
	xs := make([]otogi.Extension, n)
	for i := range xs {
		ns, e := r.str(4096)
		if e != nil {
			return nil, e
		}
		name, e := r.str(4096)
		if e != nil {
			return nil, e
		}
		v, e := r.u32()
		if e != nil {
			return nil, e
		}
		media, e := r.str(4096)
		if e != nil {
			return nil, e
		}
		data, e := r.blob(64 * 1024)
		if e != nil {
			return nil, e
		}
		xs[i], e = otogi.NewExtension(ns, name, v, media, data)
		if e != nil {
			return nil, e
		}
	}
	return xs, nil
}
func readIDs(r *eventReader, max int) ([]otogi.PartID, error) {
	n, e := r.count(max)
	if e != nil {
		return nil, e
	}
	xs := make([]otogi.PartID, n)
	for i := range xs {
		xs[i], e = readID(r, otogi.NewPartID)
		if e != nil {
			return nil, e
		}
	}
	return xs, nil
}
func readMessage(r *eventReader) (otogi.Message, error) {
	roots, e := readIDs(r, 256)
	if e != nil {
		return otogi.Message{}, e
	}
	n, e := r.count(256)
	if e != nil {
		return otogi.Message{}, e
	}
	parts := make([]otogi.Part, n)
	for i := range parts {
		parts[i], e = readPart(r)
		if e != nil {
			return otogi.Message{}, e
		}
	}
	n, e = r.count(64)
	if e != nil {
		return otogi.Message{}, e
	}
	rels := make([]otogi.Relation, n)
	for i := range rels {
		rels[i], e = readRelation(r)
		if e != nil {
			return otogi.Message{}, e
		}
	}
	exts, e := readExtensions(r, 64)
	if e != nil {
		return otogi.Message{}, e
	}
	return otogi.NewMessage(roots, parts, rels, exts)
}
func readPart(r *eventReader) (otogi.Part, error) {
	id, e := readID(r, otogi.NewPartID)
	if e != nil {
		return otogi.Part{}, e
	}
	ks, e := r.str(32)
	if e != nil {
		return otogi.Part{}, e
	}
	kind := otogi.PartKind(ks)
	children, e := readIDs(r, 64)
	if e != nil {
		return otogi.Part{}, e
	}
	exts, e := readExtensions(r, 64)
	if e != nil {
		return otogi.Part{}, e
	}
	switch kind {
	case otogi.PartText:
		text, e := r.str(64 * 1024)
		if e != nil {
			return otogi.Part{}, e
		}
		n, e := r.count(128)
		if e != nil {
			return otogi.Part{}, e
		}
		anns := make([]otogi.Annotation, n)
		for i := range anns {
			name, e := r.str(4096)
			if e != nil {
				return otogi.Part{}, e
			}
			off, e := r.i64()
			if e != nil || off < 0 || off > math.MaxInt {
				return otogi.Part{}, fmt.Errorf("telegram event codec: annotation offset")
			}
			ln, e := r.i64()
			if e != nil || ln < 0 || ln > math.MaxInt {
				return otogi.Part{}, fmt.Errorf("telegram event codec: annotation length")
			}
			ak, e := r.str(32)
			if e != nil {
				return otogi.Part{}, e
			}
			target, e := r.str(64 * 1024)
			if e != nil {
				return otogi.Part{}, e
			}
			aext, e := readExtensions(r, 64)
			if e != nil {
				return otogi.Part{}, e
			}
			anns[i], e = otogi.NewAnnotation(name, int(off), int(ln), otogi.AnnotationKind(ak), target, aext)
			if e != nil {
				return otogi.Part{}, e
			}
		}
		return otogi.NewTextPart(id, text, anns, children, exts)
	case otogi.PartMedia, otogi.PartFile:
		m, e := readMedia(r)
		if e != nil {
			return otogi.Part{}, e
		}
		return otogi.NewMediaPart(id, kind, m, children, exts)
	case otogi.PartLocation:
		lat, e := r.f64()
		if e != nil {
			return otogi.Part{}, e
		}
		lon, e := r.f64()
		if e != nil {
			return otogi.Part{}, e
		}
		label, e := r.str(4096)
		if e != nil {
			return otogi.Part{}, e
		}
		x, e := otogi.NewLocation(otogi.LocationParams{Latitude: lat, Longitude: lon, Label: label})
		if e != nil {
			return otogi.Part{}, e
		}
		return otogi.NewLocationPart(id, x, children, exts)
	case otogi.PartContact:
		a, e := r.str(4096)
		if e != nil {
			return otogi.Part{}, e
		}
		b, e := r.str(4096)
		if e != nil {
			return otogi.Part{}, e
		}
		x, e := otogi.NewContact(otogi.ContactParams{DisplayName: a, Address: b})
		if e != nil {
			return otogi.Part{}, e
		}
		return otogi.NewContactPart(id, x, children, exts)
	case otogi.PartPoll:
		q, e := r.str(4096)
		if e != nil {
			return otogi.Part{}, e
		}
		n, e := r.count(64)
		if e != nil {
			return otogi.Part{}, e
		}
		opts := make([]string, n)
		for i := range opts {
			opts[i], e = r.str(4096)
			if e != nil {
				return otogi.Part{}, e
			}
		}
		multi, e := r.u8()
		if e != nil || multi > 1 {
			return otogi.Part{}, fmt.Errorf("telegram event codec: invalid boolean")
		}
		x, e := otogi.NewPoll(otogi.PollParams{Question: q, Options: opts, Multiple: multi == 1})
		if e != nil {
			return otogi.Part{}, e
		}
		return otogi.NewPollPart(id, x, children, exts)
	case otogi.PartInteractive:
		layout, e := r.str(4096)
		if e != nil {
			return otogi.Part{}, e
		}
		action, e := r.str(4096)
		if e != nil {
			return otogi.Part{}, e
		}
		label, e := r.str(4096)
		if e != nil {
			return otogi.Part{}, e
		}
		x, e := otogi.NewInteractive(otogi.InteractiveParams{Layout: layout, ActionID: action, Label: label})
		if e != nil {
			return otogi.Part{}, e
		}
		return otogi.NewInteractivePart(id, x, children, exts)
	case otogi.PartEmbeddedMessage:
		summary, e := r.str(4096)
		if e != nil {
			return otogi.Part{}, e
		}
		ref, e := r.str(4096)
		if e != nil {
			return otogi.Part{}, e
		}
		x, e := otogi.NewEmbeddedMessage(otogi.EmbeddedMessageParams{Summary: summary, Reference: ref})
		if e != nil {
			return otogi.Part{}, e
		}
		return otogi.NewEmbeddedMessagePart(id, x, children, exts)
	case otogi.PartOpaque:
		values, e := readExtensions(r, 1)
		if e != nil || len(values) != 1 {
			return otogi.Part{}, fmt.Errorf("telegram event codec: invalid opaque value")
		}
		return otogi.NewOpaquePart(id, values[0], children, exts)
	default:
		return otogi.Part{}, fmt.Errorf("telegram event codec: unknown part kind")
	}
}
func readMedia(r *eventReader) (otogi.Media, error) {
	role, e := r.str(32)
	if e != nil {
		return otogi.Media{}, e
	}
	mediaType, e := r.str(4096)
	if e != nil {
		return otogi.Media{}, e
	}
	filename, e := r.str(64 * 1024)
	if e != nil {
		return otogi.Media{}, e
	}
	size, e := r.i64()
	if e != nil {
		return otogi.Media{}, e
	}
	width, e := r.i64()
	if e != nil || width < 0 || width > math.MaxInt {
		return otogi.Media{}, fmt.Errorf("telegram event codec: width")
	}
	height, e := r.i64()
	if e != nil || height < 0 || height > math.MaxInt {
		return otogi.Media{}, fmt.Errorf("telegram event codec: height")
	}
	duration, e := r.i64()
	if e != nil {
		return otogi.Media{}, e
	}
	n, e := r.count(32)
	if e != nil {
		return otogi.Media{}, e
	}
	hashes := make([]otogi.MediaHash, n)
	for i := range hashes {
		alg, e := r.str(4096)
		if e != nil {
			return otogi.Media{}, e
		}
		value, e := r.blob(1024 * 1024)
		if e != nil {
			return otogi.Media{}, e
		}
		hashes[i], e = otogi.NewMediaHash(alg, value)
		if e != nil {
			return otogi.Media{}, e
		}
	}
	sk, e := r.str(32)
	if e != nil {
		return otogi.Media{}, e
	}
	var source otogi.MediaSource
	switch otogi.MediaSourceKind(sk) {
	case otogi.MediaSourceInline:
		b, e := r.blob(256 * 1024)
		if e != nil {
			return otogi.Media{}, e
		}
		source, e = otogi.NewInlineMediaSource(b, nil)
		if e != nil {
			return otogi.Media{}, e
		}
	case otogi.MediaSourceExternal:
		u, e := r.str(64 * 1024)
		if e != nil {
			return otogi.Media{}, e
		}
		source, e = otogi.NewExternalMediaSource(u, nil)
		if e != nil {
			return otogi.Media{}, e
		}
	case otogi.MediaSourceOpaque:
		l, e := readLocator(r)
		if e != nil {
			return otogi.Media{}, e
		}
		source, e = otogi.NewOpaqueMediaSource(l, nil)
		if e != nil {
			return otogi.Media{}, e
		}
	default:
		return otogi.Media{}, fmt.Errorf("telegram event codec: unknown media source")
	}
	sourceExt, e := readExtensions(r, 64)
	if e != nil {
		return otogi.Media{}, e
	}
	switch source.Kind() {
	case otogi.MediaSourceInline:
		b, _ := source.Inline()
		source, e = otogi.NewInlineMediaSource(b, sourceExt)
	case otogi.MediaSourceExternal:
		u, _ := source.ExternalURI()
		source, e = otogi.NewExternalMediaSource(u, sourceExt)
	case otogi.MediaSourceOpaque:
		l, _ := source.OpaqueLocator()
		source, e = otogi.NewOpaqueMediaSource(l, sourceExt)
	}
	if e != nil {
		return otogi.Media{}, e
	}
	exts, e := readExtensions(r, 64)
	if e != nil {
		return otogi.Media{}, e
	}
	return otogi.NewMedia(otogi.MediaParams{Role: otogi.MediaRole(role), MediaType: mediaType, Filename: filename, ByteSize: size, Width: int(width), Height: int(height), Duration: time.Duration(duration), Hashes: hashes, Source: source, Extensions: exts})
}
func readRelation(r *eventReader) (otogi.Relation, error) {
	kind, e := r.str(32)
	if e != nil {
		return otogi.Relation{}, e
	}
	platform, e := readID(r, otogi.NewPlatformID)
	if e != nil {
		return otogi.Relation{}, e
	}
	account, e := readID(r, otogi.NewAccountID)
	if e != nil {
		return otogi.Relation{}, e
	}
	conversation, e := readID(r, otogi.NewConversationID)
	if e != nil {
		return otogi.Relation{}, e
	}
	source, e := readID(r, otogi.NewSourceEventID)
	if e != nil {
		return otogi.Relation{}, e
	}
	locator, e := readLocator(r)
	if e != nil {
		return otogi.Relation{}, e
	}
	target, e := otogi.NewEventRef(otogi.EventRefParams{Platform: platform, Account: account, Conversation: conversation, SourceEvent: source, Locator: locator})
	if e != nil {
		return otogi.Relation{}, e
	}
	key, e := r.str(64 * 1024)
	if e != nil {
		return otogi.Relation{}, e
	}
	exts, e := readExtensions(r, 64)
	if e != nil {
		return otogi.Relation{}, e
	}
	return otogi.NewRelation(otogi.RelationKind(kind), target, key, exts)
}
