package lanes

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// knownGoodMax bounds the remembered list: enough for any pool target, small
// enough that a restart's first burst goes to servers that recently worked.
const knownGoodMax = 200

type knownFile struct {
	Version int       `json:"version"`
	Saved   time.Time `json:"saved"`
	Lanes   []string  `json:"lanes"`
}

// LoadKnownGood restores the lanes that worked before a restart from path. A
// missing or unreadable file just means starting cold.
func LoadKnownGood(m *Manager, path string, log *slog.Logger) {
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn("known lanes: read failed; starting cold", "path", path, "err", err)
		}
		return
	}
	var f knownFile
	if err := json.Unmarshal(b, &f); err != nil {
		log.Warn("known lanes: bad file; starting cold", "path", path, "err", err)
		return
	}
	m.Remember(f.Lanes)
	log.Info("known lanes restored", "lanes", len(f.Lanes), "saved", f.Saved)
}

// SaveKnownGood writes the lanes that have worked to path every interval, and
// once more when ctx ends, so the next start reconnects to them first. Writes
// are atomic (temp file + rename) and skipped when nothing changed.
func SaveKnownGood(ctx context.Context, m *Manager, path string, every time.Duration, log *slog.Logger) {
	var last []string
	save := func() {
		ids := m.KnownGood(knownGoodMax)
		if len(ids) == 0 || slices.Equal(ids, last) {
			return
		}
		if err := writeKnown(path, ids); err != nil {
			log.Warn("known lanes: save failed", "path", path, "err", err)
			return
		}
		last = ids
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			save()
			return
		case <-t.C:
			save()
		}
	}
}

func writeKnown(path string, ids []string) error {
	b, err := json.Marshal(knownFile{Version: 1, Saved: time.Now().UTC(), Lanes: ids})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".known-lanes-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
