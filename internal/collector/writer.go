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

// Forgetter erfaehrt, welche Objekte nicht gespeichert werden konnten.
type Forgetter interface {
	Forget(uids ...string)
}

// WriterOptions konfiguriert die Stapelbildung.
type WriterOptions struct {
	BatchSize     int
	FlushInterval time.Duration
	// OnFailure bekommt die UIDs eines gescheiterten Stapels. Ohne das wuerde
	// der Hash-Cache des Handlers glauben, diese Versionen seien gespeichert,
	// und die naechste identische Beobachtung wegdedupliziert.
	OnFailure Forgetter
	// ShutdownFlushTimeout begrenzt den Abschluss-Flush.
	ShutdownFlushTimeout time.Duration
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
	if opts.ShutdownFlushTimeout <= 0 {
		opts.ShutdownFlushTimeout = 10 * time.Second
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

	// flushCtx ist ein Parameter, weil der Abschluss-Flush einen anderen
	// Context braucht als der laufende Betrieb.
	flush := func(flushCtx context.Context) {
		if len(batch) == 0 {
			return
		}
		if err := w.sink.WriteChanges(flushCtx, batch); err != nil {
			writeFailures.Inc()
			logger.Error(err, "Aenderungen nicht gespeichert", "count", len(batch))
			// Der Handler muss die Hashes wieder vergessen, sonst gilt eine nie
			// geschriebene Version als gespeichert und die naechste Beobachtung
			// desselben Zustands erzeugt keinen Eintrag mehr.
			if w.opts.OnFailure != nil {
				uids := make([]string, 0, len(batch))
				for _, c := range batch {
					uids = append(uids, c.UID)
				}
				w.opts.OnFailure.Forget(uids...)
			}
		} else {
			changesWritten.Add(float64(len(batch)))
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			// Der laufende Context ist hier bereits abgelaufen; mit ihm wuerde
			// pool.Begin sofort scheitern und der letzte Stapel waere immer
			// verloren - bei Leader-Uebergabe also jedes Mal.
			flushCtx, cancel := context.WithTimeout(
				context.WithoutCancel(ctx), w.opts.ShutdownFlushTimeout)
			flush(flushCtx)
			cancel()
			return nil
		case change, open := <-w.in:
			if !open {
				flush(ctx)
				return nil
			}
			batch = append(batch, change)
			if len(batch) >= w.opts.BatchSize {
				flush(ctx)
			}
		case <-ticker.C:
			flush(ctx)
		}
	}
}
