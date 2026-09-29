package collector

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/Fuchsi94/stateinspector/internal/store"
)

var (
	// changesWritten zaehlt erfolgreich gespeicherte Aenderungen.
	changesWritten = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "stateinspector_changes_written_total",
		Help: "Anzahl erfolgreich gespeicherter Aenderungen.",
	})
	// writeFailures macht sichtbar, dass Aenderungen nicht ankamen.
	writeFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "stateinspector_write_failures_total",
		Help: "Anzahl fehlgeschlagener Schreibvorgaenge.",
	})
	// bufferFull zeigt Rueckstau: der Handler musste auf den Writer warten.
	bufferFull = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "stateinspector_buffer_full_total",
		Help: "Wie oft der Puffer voll war und der Event-Handler blockieren musste.",
	})
)

func init() {
	metrics.Registry.MustRegister(changesWritten, writeFailures, bufferFull)
}

// WriterOptions konfiguriert die Stapelbildung.
type WriterOptions struct {
	BatchSize     int
	FlushInterval time.Duration
}

// Writer sammelt Aenderungen und schreibt sie gebuendelt, damit der
// Event-Handler nie auf die Datenbank wartet.
type Writer struct {
	sink Sink
	in   <-chan store.Change
	opts WriterOptions
}

// NewWriter baut den Writer.
func NewWriter(sink Sink, in <-chan store.Change, opts WriterOptions) *Writer {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 100
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = time.Second
	}
	return &Writer{sink: sink, in: in, opts: opts}
}

// NeedLeaderElection sorgt dafuer, dass nur der Leader schreibt (R3).
func (w *Writer) NeedLeaderElection() bool { return true }

// Run laeuft, bis der Kanal geschlossen oder der Context beendet ist. Ein
// Schreibfehler beendet den Writer nicht: sonst stuende die Historie nach
// einem einzelnen Datenbankschluckauf dauerhaft still.
func (w *Writer) Run(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("writer")
	ticker := time.NewTicker(w.opts.FlushInterval)
	defer ticker.Stop()

	batch := make([]store.Change, 0, w.opts.BatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := w.sink.WriteChanges(ctx, batch); err != nil {
			writeFailures.Inc()
			logger.Error(err, "Aenderungen nicht gespeichert", "count", len(batch))
		} else {
			changesWritten.Add(float64(len(batch)))
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return nil
		case change, open := <-w.in:
			if !open {
				flush()
				return nil
			}
			batch = append(batch, change)
			if len(batch) >= w.opts.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}
