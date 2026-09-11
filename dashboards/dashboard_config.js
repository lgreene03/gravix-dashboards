// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Placeholder. This file exists so that a mountpoint exists.
//
// docker-compose.bootstrap.yml mounts ./dashboards read-only at
// /usr/share/nginx/html, then mounts the generated ./data/dashboard_config.js
// over this path. Docker cannot create a mountpoint inside a read-only mount,
// so without a file here the dashboard container refuses to start:
//
//   error mounting ".../data/dashboard_config.js" to rootfs at
//   "/usr/share/nginx/html/dashboard_config.js": make mountpoint:
//   openat dashboard_config.js: read-only file system
//
// See F-023. bootstrap_seed writes the real file into ./data/ on first boot;
// this one is never served when the stack is running.
//
// Left deliberately empty rather than filled with defaults: app.js merges
// window.GRAVIX_CONFIG over its own built-ins, so an empty object means an
// un-provisioned dashboard behaves exactly as it would with no config at all,
// instead of appearing configured with values nobody provisioned.
window.GRAVIX_CONFIG = {};
