package kernel

import (
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

const historyCapacity = 64

// Delivery records one module's terminal outcome for an admitted event.
type Delivery struct {
	eventID  otogi.EventID
	moduleID otogi.ModuleID
	status   otogi.ResultStatus
	err      error
}

// EventID returns the delivered event identifier.
func (d Delivery) EventID() otogi.EventID { return d.eventID }

// ModuleID returns the invoked module identifier.
func (d Delivery) ModuleID() otogi.ModuleID { return d.moduleID }

// Status returns the module's terminal status.
func (d Delivery) Status() otogi.ResultStatus { return d.status }

// Err returns the stable failure identity, or nil for success and rejection.
func (d Delivery) Err() error { return d.err }

// PanicReport identifies a recovered module panic without retaining its value or event content.
type PanicReport struct {
	eventID  otogi.EventID
	moduleID otogi.ModuleID
	stack    []byte
}

// EventID returns the event whose module invocation panicked.
func (r PanicReport) EventID() otogi.EventID { return r.eventID }

// ModuleID returns the module whose invocation panicked.
func (r PanicReport) ModuleID() otogi.ModuleID { return r.moduleID }

// Stack returns an independent copy of the bounded recovery stack.
func (r PanicReport) Stack() []byte { return append([]byte(nil), r.stack...) }

// Deliveries returns delivery outcomes from oldest to newest as an independent slice.
func (k *Kernel) Deliveries() []Delivery {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]Delivery(nil), k.deliveries...)
}

// Panics returns panic reports from oldest to newest with independent stacks.
func (k *Kernel) Panics() []PanicReport {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make([]PanicReport, len(k.panics))
	for i, report := range k.panics {
		out[i] = PanicReport{eventID: report.eventID, moduleID: report.moduleID, stack: append([]byte(nil), report.stack...)}
	}
	return out
}

func appendBounded[T any](items []T, item T) []T {
	if len(items) == historyCapacity {
		copy(items, items[1:])
		items[len(items)-1] = item
		return items
	}
	return append(items, item)
}
