package flow

import (
	"fmt"
	"strings"
)

// Stepper is an arithmetic-progression iterator over [start, end) with the given
// step. Built from a string ("start...end" or "start:end:step") and used by the loop
// gateway's $in. Mirrors Java's Stepper.
type Stepper struct {
	start, end, step int
	nextValue        int
	hasMore          bool
}

// StepperFrom parses str ("start...end" with step 1, or "start:end:step").
func StepperFrom(str string) (*Stepper, error) {
	if idx := strings.Index(str, "..."); idx > 0 {
		startStr := str[:idx]
		endStr := str[idx+3:]
		start, err1 := atoi(startStr)
		end, err2 := atoi(endStr)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("flow: stepper parameters must be integers: %s", str)
		}
		return newStepper(start, end, 1)
	}
	terms := strings.SplitN(str, ":", 3)
	if len(terms) != 3 {
		return nil, fmt.Errorf("flow: stepper style must be 'start...end' or 'start:end:step'")
	}
	start, err1 := atoi(terms[0])
	end, err2 := atoi(terms[1])
	step, err3 := atoi(terms[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return nil, fmt.Errorf("flow: stepper parameters must be integers: %s", str)
	}
	return newStepper(start, end, step)
}

func newStepper(start, end, step int) (*Stepper, error) {
	if step <= 0 {
		return nil, fmt.Errorf("flow: stepper step must be positive")
	}
	return &Stepper{start: start, end: end, step: step, nextValue: start, hasMore: start < end}, nil
}

// HasNext reports whether more values remain.
func (s *Stepper) HasNext() bool { return s.hasMore }

// Next returns the next value. Call HasNext first.
func (s *Stepper) Next() int {
	result := s.nextValue
	if s.nextValue < s.end-s.step {
		s.nextValue += s.step
	} else {
		s.hasMore = false
	}
	return result
}

func atoi(s string) (int, error) {
	s = strings.TrimSpace(s)
	var n int
	var sign int = 1
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		if s[i] == '-' {
			sign = -1
		}
		i++
	}
	if i == len(s) {
		return 0, fmt.Errorf("empty")
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(s[i]-'0')
	}
	return n * sign, nil
}
