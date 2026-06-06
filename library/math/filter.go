package math

import (
	"fmt"
	"math"
)

type ExpFilter struct {
	alpha    float64
	filtered *float64
	maxVal   *float64
	minVal   *float64
}

type ExpFilterOptions struct {
	MaxVal  *float64
	MinVal  *float64
	Initial *float64
}

func NewExpFilter(alpha float64, maxVal float64) *ExpFilter {
	options := ExpFilterOptions{}
	if maxVal != -1.0 {
		options.MaxVal = &maxVal
	}
	filter, err := NewExpFilterWithOptions(alpha, options)
	if err != nil {
		panic(err)
	}
	return filter
}

func NewExpFilterWithOptions(alpha float64, options ExpFilterOptions) (*ExpFilter, error) {
	if err := validateAlpha(alpha); err != nil {
		return nil, err
	}
	return &ExpFilter{
		alpha:    alpha,
		filtered: cloneFloat(options.Initial),
		maxVal:   cloneFloat(options.MaxVal),
		minVal:   cloneFloat(options.MinVal),
	}, nil
}

func (e *ExpFilter) Reset(alpha float64) {
	if alpha != -1.0 {
		if err := validateAlpha(alpha); err != nil {
			panic(err)
		}
		e.alpha = alpha
		return
	}
	e.filtered = nil
}

func (e *ExpFilter) ResetWithOptions(options ExpFilterOptions) error {
	if options.Initial != nil {
		e.filtered = cloneFloat(options.Initial)
	}
	if options.MinVal != nil {
		e.minVal = cloneFloat(options.MinVal)
	}
	if options.MaxVal != nil {
		e.maxVal = cloneFloat(options.MaxVal)
	}
	return nil
}

func (e *ExpFilter) Apply(exp float64, sample float64) float64 {
	if e.filtered == nil {
		e.filtered = cloneFloat(&sample)
	} else {
		a := math.Pow(e.alpha, exp)
		filtered := a**e.filtered + (1-a)*sample
		e.filtered = &filtered
	}
	e.applyBounds()
	return *e.filtered
}

func (e *ExpFilter) ApplyWithoutSample(exp float64) (float64, error) {
	if e.filtered == nil {
		return 0, fmt.Errorf("sample or initial value must be given")
	}
	sample := *e.filtered
	return e.Apply(exp, sample), nil
}

func (e *ExpFilter) applyBounds() {
	if e.filtered == nil {
		return
	}
	if e.maxVal != nil && *e.filtered > *e.maxVal {
		e.filtered = cloneFloat(e.maxVal)
	}
	if e.minVal != nil && *e.filtered < *e.minVal {
		e.filtered = cloneFloat(e.minVal)
	}
}

func (e *ExpFilter) Filtered() float64 {
	if e.filtered == nil {
		return -1.0
	}
	return *e.filtered
}

func (e *ExpFilter) Value() (float64, bool) {
	if e.filtered == nil {
		return 0, false
	}
	return *e.filtered, true
}

func (e *ExpFilter) UpdateBase(alpha float64) {
	if err := validateAlpha(alpha); err != nil {
		panic(err)
	}
	e.alpha = alpha
}

func validateAlpha(alpha float64) error {
	if alpha <= 0 || alpha > 1 {
		return fmt.Errorf("alpha must be in (0, 1]")
	}
	return nil
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
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
