package utils

import (
	"context"
	"errors"
	"sync"
	"time"
)

type CircuitBreaker struct {
	name         string
	maxRequests  uint32
	interval     time.Duration
	timeout      time.Duration
	failureRatio float64

	mutex  sync.RWMutex
	state  State
	counts Counts
	expiry time.Time
}

type State int

const (
	StateClosed State = iota
	StateHalfOpen
	StateOpen
)

type Counts struct {
	Requests             uint32
	TotalSuccesses       uint32
	TotalFailures        uint32
	ConsecutiveSuccesses uint32
	ConsecutiveFailures  uint32
}

func NewCircuitBreaker(name string) *CircuitBreaker {
	return &CircuitBreaker{
		name:         name,
		maxRequests:  100,
		interval:     60 * time.Second,
		timeout:      60 * time.Second,
		failureRatio: 0.6,
		state:        StateClosed,
	}
}

func (cb *CircuitBreaker) Execute(ctx context.Context, req func() (interface{}, error)) (interface{}, error) {
	generation, err := cb.beforeRequest()
	if err != nil {
		return nil, err
	}

	defer func() {
		e := recover()
		if e != nil {
			cb.afterRequest(generation, false)
			panic(e)
		}
	}()

	result, err := req()
	cb.afterRequest(generation, err == nil)
	return result, err
}

func (cb *CircuitBreaker) beforeRequest() (uint64, error) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	state, generation := cb.currentState(now)

	if state == StateOpen {
		return generation, errors.New("circuit breaker is open")
	} else if state == StateHalfOpen && cb.counts.Requests >= cb.maxRequests {
		return generation, errors.New("too many requests when circuit breaker is half open")
	}

	cb.counts.Requests++
	return generation, nil
}

func (cb *CircuitBreaker) afterRequest(before uint64, success bool) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	state, generation := cb.currentState(now)
	if generation != before {
		return
	}

	if success {
		cb.onSuccess(state)
	} else {
		cb.onFailure(state)
	}
}

func (cb *CircuitBreaker) onSuccess(state State) {
	cb.counts.TotalSuccesses++
	cb.counts.ConsecutiveSuccesses++
	cb.counts.ConsecutiveFailures = 0

	if state == StateHalfOpen {
		cb.state = StateClosed
	}
}

func (cb *CircuitBreaker) onFailure(state State) {
	cb.counts.TotalFailures++
	cb.counts.ConsecutiveFailures++
	cb.counts.ConsecutiveSuccesses = 0

	if cb.readyToTrip() {
		cb.state = StateOpen
		cb.expiry = time.Now().Add(cb.timeout)
	}
}

func (cb *CircuitBreaker) readyToTrip() bool {
	return cb.counts.Requests >= cb.maxRequests &&
		float64(cb.counts.TotalFailures)/float64(cb.counts.Requests) >= cb.failureRatio
}

func (cb *CircuitBreaker) currentState(now time.Time) (State, uint64) {
	switch cb.state {
	case StateClosed:
		if !cb.expiry.IsZero() && cb.expiry.Before(now) {
			cb.toNewGeneration(now)
		}
	case StateOpen:
		if cb.expiry.Before(now) {
			cb.state = StateHalfOpen
			cb.toNewGeneration(now)
		}
	}
	return cb.state, 0
}

func (cb *CircuitBreaker) toNewGeneration(now time.Time) {
	cb.counts = Counts{}

	var zero time.Time
	switch cb.state {
	case StateClosed:
		cb.expiry = now.Add(cb.interval)
	case StateOpen:
		cb.expiry = zero
	default:
		cb.expiry = zero
	}
}
