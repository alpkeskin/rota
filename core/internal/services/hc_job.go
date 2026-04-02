package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/google/uuid"
)

// HCJobStatus represents the state of a health-check job
type HCJobStatus string

const (
	HCJobPending  HCJobStatus = "pending"
	HCJobRunning  HCJobStatus = "running"
	HCJobDone     HCJobStatus = "done"
	HCJobFailed   HCJobStatus = "failed"
)

// HCJob holds state for one async pool health-check run
type HCJob struct {
	ID        string      `json:"id"`
	PoolID    int         `json:"pool_id"`
	PoolName  string      `json:"pool_name"`
	Status    HCJobStatus `json:"status"`
	Progress  int         `json:"progress"`  // checked so far
	Total     int         `json:"total"`     // total proxies
	Active    int         `json:"active"`
	Failed    int         `json:"failed"`
	CheckURL  string      `json:"check_url"`
	Workers   int         `json:"workers"`
	Error     string      `json:"error,omitempty"`
	StartedAt time.Time   `json:"started_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// Full results (populated when done)
	Results []models.ProxyTestResult `json:"results,omitempty"`
}

// HCJobStore keeps in-memory map of recent jobs (TTL 30 min)
type HCJobStore struct {
	mu   sync.RWMutex
	jobs map[string]*HCJob
}

var globalJobStore = &HCJobStore{
	jobs: make(map[string]*HCJob),
}

// GetJobStore returns the singleton job store
func GetJobStore() *HCJobStore {
	return globalJobStore
}

// Create registers a new job and returns it
func (s *HCJobStore) Create(poolID int, poolName, checkURL string, workers int) *HCJob {
	job := &HCJob{
		ID:        uuid.New().String(),
		PoolID:    poolID,
		PoolName:  poolName,
		Status:    HCJobPending,
		CheckURL:  checkURL,
		Workers:   workers,
		StartedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	s.mu.Lock()
	s.jobs[job.ID] = job
	s.mu.Unlock()

	// Cleanup old jobs in background
	go s.cleanup()
	return job
}

// Get returns a job by ID
func (s *HCJobStore) Get(id string) (*HCJob, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.jobs[id]
	return j, ok
}

// Update mutates a job (caller must hold no lock)
func (s *HCJobStore) Update(id string, fn func(*HCJob)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		fn(j)
		j.UpdatedAt = time.Now()
	}
}

// cleanup removes jobs older than 30 minutes
func (s *HCJobStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-30 * time.Minute)
	for id, j := range s.jobs {
		if j.StartedAt.Before(cutoff) {
			delete(s.jobs, id)
		}
	}
}

// ListByPool returns all jobs for a given pool (newest first)
func (s *HCJobStore) ListByPool(poolID int) []*HCJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*HCJob
	for _, j := range s.jobs {
		if j.PoolID == poolID {
			out = append(out, j)
		}
	}
	// sort newest first
	for i := 0; i < len(out)-1; i++ {
		for k := i + 1; k < len(out); k++ {
			if out[k].StartedAt.After(out[i].StartedAt) {
				out[i], out[k] = out[k], out[i]
			}
		}
	}
	return out
}

// RunPoolHealthCheckAsync starts the health check in a goroutine and returns job_id immediately.
// It calls poolSvc.HealthCheckPoolWithProgress which updates the job store as proxies are checked.
func RunPoolHealthCheckAsync(
	ctx context.Context,
	poolSvc *PoolService,
	poolID int,
	poolName, checkURL string,
	workers int,
) (*HCJob, error) {
	store := GetJobStore()
	if poolName == "" {
		poolName = fmt.Sprintf("Pool #%d", poolID)
	}
	if workers <= 0 {
		workers = 20
	}

	// Get proxy count upfront so frontend can show progress %
	proxies, _ := poolSvc.poolRepo.GetProxies(ctx, poolID)

	job := store.Create(poolID, poolName, checkURL, workers)
	store.Update(job.ID, func(j *HCJob) {
		j.Total = len(proxies)
	})

	go func() {
		store.Update(job.ID, func(j *HCJob) {
			j.Status = HCJobRunning
		})

		result, err := poolSvc.HealthCheckPoolWithProgress(
			context.Background(), // use background so UI disconnect doesn't kill it
			poolID, checkURL, workers,
			func(checked, active, failed int) {
				store.Update(job.ID, func(j *HCJob) {
					j.Progress = checked
					j.Active = active
					j.Failed = failed
				})
			},
		)

		now := time.Now()
		if err != nil {
			store.Update(job.ID, func(j *HCJob) {
				j.Status = HCJobFailed
				j.Error = err.Error()
				j.FinishedAt = &now
			})
			return
		}

		store.Update(job.ID, func(j *HCJob) {
			j.Status = HCJobDone
			j.Total = result.Checked
			j.Active = result.Active
			j.Failed = result.Failed
			j.Progress = result.Checked
			j.Results = result.Results
			j.FinishedAt = &now
		})
	}()

	return job, nil
}
