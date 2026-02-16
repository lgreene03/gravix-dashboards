# System Constitution

This document defines the invariants of the system. These rules are absolute and must not be violated.

## 1. Definition of a Fact

A **Fact** is the atomic unit of storage.

- A Fact is a structured record of a single event or state change at a specific point in time.
- A Fact MUST contain a timestamp (`event_time`).
- A Fact MUST contain a schema version.
- A Fact MUST NOT contain derived data.

## 2. Immutability

- Once a Fact is persisted to storage (Iceberg), it is **IMMUTABLE**.
- Facts CANNOT be updated.
- Facts CANNOT be deleted, except for:
  - Legal compliance (e.g., GDPR/CCPA).
  - Data retention policies (TTL).
- Corrections are handled by appending new Facts that supersede or negate previous ones (e.g., via a semantic view layer), NEVER by mutating storage.

## 3. Append-Only Storage

- The storage layer interaction is strictly **APPEND-ONLY**.
- Writers MUST NOT issue `UPDATE` or `DELETE` statements against the Fact tables.
- Partition rewriting is permitted ONLY for:
  - Compaction (optimizing file size/layout).
  - Schema evolution (adding columns).
  - Deduplication (resolving at-least-once delivery duplicates).

## 4. Recomputability

- **Derivatives are disposable.**
- Any metric, dashboard, or aggregate table MUST be reproducible from the raw Facts.
- If the semantic layer or query logic changes, all downstream data MUST be recomputable from source Facts.
- The system MUST support full historical reprocessing without data loss.

## 5. Invariants

- **Schema Enforcement**: All Facts must conform to a strict schema before ingestion.
- **Time Monotonicity**: Events can arrive late, but `event_time` is the only source of truth for ordering.
- **No Nulls in Keys**: Partition keys and primary identifiers MUST NOT be null.

## 6. Safety & Security

- **Authentication**: All ingestion requests MUST be authenticated via API Key.
- **Data Cleanup**: Raw and Warehouse data older than 30 days MUST be automatically purged to prevent resource exhaustion.
- **Fail-Safe**: Ingestion service MUST buffer to disk before acknowledging receipt to prevent data loss during crashes.
