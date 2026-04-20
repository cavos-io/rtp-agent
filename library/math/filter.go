package math

import "math"

type ExpFilter struct {
	alpha    float64
	filtered float64
	maxVal   float64
}

func NewExpFilter(alpha float64, maxVal float64) *ExpFilter {
	return &ExpFilter{
		alpha:    alpha,
		filtered: -1.0,
		maxVal:   maxVal,
	}
}

func (e *ExpFilter) Reset(alpha float64) {
	if alpha != -1.0 {
		e.alpha = alpha
	}
	e.filtered = -1.0
}

func (e *ExpFilter) Apply(exp float64, sample float64) float64 {
	if e.filtered == -1.0 {
		e.filtered = sample
	} else {
		a := math.Pow(e.alpha, exp)
		e.filtered = a*e.filtered + (1-a)*sample
	}

	if e.maxVal != -1.0 && e.filtered > e.maxVal {
		e.filtered = e.maxVal
	}

	return e.filtered
}

func (e *ExpFilter) Filtered() float64 {
	return e.filtered
}

func (e *ExpFilter) UpdateBase(alpha float64) {
	e.alpha = alpha
}

type MovingAverage struct {
	hist  []float64
	sum   float64
	count int
}

func NewMovingAverage(windowSize int) *MovingAverage {
	return &MovingAverage{
		hist:  make([]float64, windowSize),
		sum:   0,
		count: 0,
	}
}

func (m *MovingAverage) AddSample(sample float64) {
	m.count++
	index := m.count % len(m.hist)
	if m.count > len(m.hist) {
		m.sum -= m.hist[index]
	}
	m.sum += sample
	m.hist[index] = sample
}

func (m *MovingAverage) GetAvg() float64 {
	if m.count == 0 {
		return 0
	}
	return m.sum / float64(m.Size())
}

func (m *MovingAverage) Reset() {
	m.count = 0
	m.sum = 0
	for i := range m.hist {
		m.hist[i] = 0
	}
}

func (m *MovingAverage) Size() int {
	if m.count < len(m.hist) {
		return m.count
	}
	return len(m.hist)
}

