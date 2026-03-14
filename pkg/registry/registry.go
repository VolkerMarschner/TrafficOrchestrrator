// Package registry tracks the state of all agents that have ever connected
// to this master and persists the data to a JSON file on disk.
package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"text/tabwriter"
	"time"
)

// AgentRecord holds the last-known state of a single agent.
type AgentRecord struct {
	ID        string    `json:"id"`
	Hostname  string    `json:"hostname"`
	IP        string    `json:"ip"`
	Version   string    `json:"version"`
	Platform  string    `json:"platform"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Status    string    `json:"status"` // "online" | "offline"
}

// Registry persists agent records to a JSON file.
type Registry struct {
	mu      sync.RWMutex
	records map[string]*AgentRecord
	path    string
}

// New creates a Registry backed by path.
// Existing data is loaded if the file is present; a missing file is not an error.
func New(path string) (*Registry, error) {
	r := &Registry{
		records: make(map[string]*AgentRecord),
		path:    path,
	}
	if err := r.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("registry: load %q: %w", path, err)
	}
	return r, nil
}

// Upsert inserts or updates a record and persists the file immediately.
// FirstSeen is preserved for existing records.
func (r *Registry) Upsert(rec AgentRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.records[rec.ID]; ok {
		rec.FirstSeen = existing.FirstSeen
	} else {
		rec.FirstSeen = time.Now()
	}
	r.records[rec.ID] = &rec
	_ = r.save()
}

// UpdateSeen refreshes LastSeen and optionally the version for an existing agent.
func (r *Registry) UpdateSeen(id, version string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec, ok := r.records[id]; ok {
		rec.LastSeen = time.Now()
		if version != "" {
			rec.Version = version
		}
		_ = r.save()
	}
}

// SetOffline marks an agent as offline and persists the file.
func (r *Registry) SetOffline(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec, ok := r.records[id]; ok {
		rec.Status = "offline"
		_ = r.save()
	}
}

// All returns a snapshot of all agent records sorted by ID.
func (r *Registry) All() []*AgentRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*AgentRecord, 0, len(r.records))
	for _, rec := range r.records {
		cp := *rec
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// PrintTable writes a human-readable tabular overview of all agents to w.
func (r *Registry) PrintTable(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tHOSTNAME\tIP\tVERSION\tPLATFORM\tSTATUS\tLAST SEEN")
	fmt.Fprintln(tw, "--\t--------\t--\t-------\t--------\t------\t---------")
	for _, rec := range r.All() {
		lastSeen := "never"
		if !rec.LastSeen.IsZero() {
			lastSeen = rec.LastSeen.Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			rec.ID, rec.Hostname, rec.IP, rec.Version,
			rec.Platform, rec.Status, lastSeen)
	}
	tw.Flush()
}

// load reads records from the JSON file. Returns os.ErrNotExist for a missing file.
func (r *Registry) load() error {
	f, err := os.Open(r.path)
	if err != nil {
		return err
	}
	defer f.Close()

	var records []*AgentRecord
	if err := json.NewDecoder(f).Decode(&records); err != nil {
		return fmt.Errorf("JSON decode: %w", err)
	}
	for _, rec := range records {
		r.records[rec.ID] = rec
	}
	return nil
}

// save writes the current registry to the JSON file.
// Caller must hold mu (read or write lock is fine — called under write lock).
func (r *Registry) save() error {
	recs := make([]*AgentRecord, 0, len(r.records))
	for _, rec := range r.records {
		recs = append(recs, rec)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].ID < recs[j].ID })

	data, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return fmt.Errorf("JSON encode: %w", err)
	}
	return os.WriteFile(r.path, data, 0600)
}
