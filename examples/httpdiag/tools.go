//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// calculatorArgs holds the input for the calculator tool.
type calculatorArgs struct {
	Operation string  `json:"operation" jsonschema:"description=Operation: add, subtract, multiply, divide, sqrt, power"`
	A         float64 `json:"a" jsonschema:"description=First operand"`
	B         float64 `json:"b,omitempty" jsonschema:"description=Second operand (optional for sqrt)"`
}

// calculatorResult holds the output of the calculator tool.
type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b,omitempty"`
	Result    float64 `json:"result"`
}

func (c *multiTurnChat) calculate(_ context.Context, args calculatorArgs) (calculatorResult, error) {
	var result float64
	switch strings.ToLower(args.Operation) {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		if args.B == 0 {
			return calculatorResult{}, fmt.Errorf("division by zero")
		}
		result = args.A / args.B
	case "sqrt":
		if args.A < 0 {
			return calculatorResult{}, fmt.Errorf("sqrt of negative number")
		}
		result = math.Sqrt(args.A)
	case "power", "pow":
		result = math.Pow(args.A, args.B)
	default:
		return calculatorResult{}, fmt.Errorf("unknown operation: %s", args.Operation)
	}
	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}, nil
}

// timeArgs holds the input for the current_time tool.
type timeArgs struct {
	Timezone string `json:"timezone,omitempty" jsonschema:"description=Timezone: UTC, EST, PST, CST, or IANA name"`
}

// timeResult holds the output of the current_time tool.
type timeResult struct {
	Time     string `json:"time"`
	Timezone string `json:"timezone"`
	Weekday  string `json:"weekday"`
}

func (c *multiTurnChat) getCurrentTime(_ context.Context, args timeArgs) (timeResult, error) {
	now := time.Now()
	tz := args.Timezone
	if tz == "" {
		tz = "Local"
	}

	var loc *time.Location
	switch strings.ToUpper(tz) {
	case "UTC":
		loc = time.UTC
	case "EST":
		loc = time.FixedZone("EST", -5*3600)
	case "PST":
		loc = time.FixedZone("PST", -8*3600)
	case "CST":
		loc, _ = time.LoadLocation("Asia/Shanghai")
	case "LOCAL":
		loc = time.Now().Location()
	default:
		var err error
		loc, err = time.LoadLocation(tz)
		if err != nil {
			return timeResult{}, fmt.Errorf("unknown timezone: %s", tz)
		}
	}

	t := now.In(loc)
	return timeResult{
		Time:     t.Format("2006-01-02 15:04:05 MST"),
		Timezone: tz,
		Weekday:  t.Weekday().String(),
	}, nil
}

func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }
