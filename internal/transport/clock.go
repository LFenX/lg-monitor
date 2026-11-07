package transport

const clockSampleCount = 6

type clockEstimate struct {
	samples []int64
	offset  int64
}

func (r *Receiver) observeClock(id string, sentAt, receivedAt int64, reset bool) (int64, bool) {
	if sentAt <= 0 {
		if reset {
			r.mu.Lock()
			delete(r.clocks, id)
			r.mu.Unlock()
		}
		return 0, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	estimate := r.clocks[id]
	if estimate == nil || reset {
		estimate = &clockEstimate{}
		r.clocks[id] = estimate
	}
	estimate.samples = append(estimate.samples, receivedAt-sentAt)
	if len(estimate.samples) > clockSampleCount {
		estimate.samples = estimate.samples[len(estimate.samples)-clockSampleCount:]
	}
	estimate.offset = estimate.samples[0]
	for _, sample := range estimate.samples[1:] {
		estimate.offset = min(estimate.offset, sample)
	}
	return estimate.offset, true
}

func (r *Receiver) ClockOffset(id string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if estimate := r.clocks[id]; estimate != nil {
		return estimate.offset
	}
	return 0
}
