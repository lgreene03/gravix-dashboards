// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package degrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

var (
	// ErrReadOnly is returned when a write is attempted in StateReadOnly.
	//
	// The text names all three facts somebody needs: what happened, what still
	// works, and what does not. An error that said only "licence expired" would
	// leave the reader to assume the worst, which at 3am is that monitoring
	// stopped.
	ErrReadOnly = errors.New("gravix enterprise: licence expired; configuration is readable and exportable, but cannot be changed")

	// ErrUnlicensed is returned when a write is attempted in StateAbsent.
	//
	// §5.2 marks the ee/ rows "n/a" for StateAbsent, because in an OSS build ee/
	// is not present to have configuration. The Enterprise binary started with no
	// licence at all is the case that table does not cover, and it must not be
	// more permissive than an expired one. It gets its own error because
	// ErrReadOnly's text says "expired", which here would be false. See SD-039.
	ErrUnlicensed = errors.New("gravix enterprise: no licence configured; configuration is readable and exportable, but cannot be changed")
)

// ExportEndpoint is where a customer gets their ee/ configuration out, named in
// every refusal so that leaving is never a thing somebody has to ask how to do.
//
// GRVX-1303 §5.3 gives "/api/gateway/exports". The gateway registers
// "/api/gateway/export"; "/api/gateway/exports" is registered only with a
// trailing segment ("/exports/scheduled"). Pointing an operator at a 404 while
// telling them their data is exportable would undo the sentence it appears in,
// so the endpoint that exists is the one named. The collision between the two
// spellings is SD-029, which is open and needs an owner, not an implementer.
const ExportEndpoint = "/api/gateway/export"

// Guard wraps an ee/ mutation. Every write path in ee/ passes through it.
//
// It runs fn in StateLicensed and StateGrace, and refuses in StateReadOnly and
// StateAbsent. It is deliberately the only way an ee/ package performs a write,
// so that "which states can write" is answered in one place and cannot drift
// between features.
//
// Nothing in the core passes through here. A core write is not an ee/ mutation,
// and adding one would be the charter §7.5 violation this spec exists to make
// impossible.
func Guard(ctx context.Context, s State, op string, fn func() error) error {
	// A nil context is a caller's bug, but this sits on every ee/ write path and
	// a panic here would turn a licence check into a crashed request. It refuses
	// or it runs; it never takes the process with it.
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	switch s {
	case StateReadOnly:
		return fmt.Errorf("%s: %w", op, ErrReadOnly)
	case StateAbsent:
		return fmt.Errorf("%s: %w", op, ErrUnlicensed)
	}
	return fn()
}

// Refused reports whether err is a refusal by Guard rather than a failure of the
// operation itself. HTTP surfaces use it to choose 402 over 500.
func Refused(err error) bool {
	return errors.Is(err, ErrReadOnly) || errors.Is(err, ErrUnlicensed)
}

// ExpiredResponse is the body every refused ee/ write returns.
//
// CoreUnaffected is in the payload rather than only in the documentation because
// the person reading it is triaging an alert and needs to know, in the response
// itself, that this is a billing matter and not an outage.
type ExpiredResponse struct {
	Error          string `json:"error"`
	Message        string `json:"message"`
	ExpiredAt      string `json:"expired_at,omitempty"`
	CoreUnaffected bool   `json:"core_unaffected"`
	ExportEndpoint string `json:"export_endpoint"`
}

// Payload builds the refusal body for a state. It is exported so an ee/ package
// that speaks something other than HTTP can render the same facts.
func Payload(s State, expiresAt time.Time) ExpiredResponse {
	r := ExpiredResponse{
		CoreUnaffected: true,
		ExportEndpoint: ExportEndpoint,
	}
	if s == StateAbsent {
		r.Error = "licence_absent"
		r.Message = "No Gravix Enterprise licence is configured. Existing configuration is " +
			"readable and exportable. Add a licence to make changes."
		return r
	}
	r.Error = "licence_expired"
	r.ExpiredAt = expiresAt.UTC().Format(time.RFC3339)
	r.Message = fmt.Sprintf("Your Gravix Enterprise licence expired on %s. Existing configuration "+
		"is readable and exportable. Renew to make changes.", expiresAt.UTC().Format("2006-01-02"))
	return r
}

// WriteRefusal sends the 402 for a refused ee/ write.
//
// 402 Payment Required, not 403: this is a commercial state, and an operator
// grepping for authorisation failures should not find it.
func WriteRefusal(w http.ResponseWriter, s State, expiresAt time.Time) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	_ = json.NewEncoder(w).Encode(Payload(s, expiresAt))
}
