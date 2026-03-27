# Usage Profile UX Design

**Date:** 2026-03-27

**Status:** Approved for specification review

## Goal

Add a profile-driven UX layer to ZenithPanel so the same product can feel optimized for different operator intents without splitting the backend, API surface, or data model into separate products.

## Problem

ZenithPanel now spans two strong domains:

- proxy operations
- VPS operations

Those domains can share one codebase, but they do not benefit from exactly the same first-run flow, navigation order, dashboard emphasis, or empty-state guidance. A single universal UI tends to become noisy for personal proxy operators and under-focused for operators who mainly want VPS tooling.

The project needs a way to adapt emphasis without creating multiple forks of the product.

## Design Summary

Introduce a stored `usage_profile` setting with three values:

- `personal_proxy`
- `vps_ops`
- `mixed`

The profile changes:

- default landing page
- dashboard card composition and order
- sidebar ordering and grouping
- which sections are shown as primary vs advanced
- setup wizard defaults and recommendations
- quick actions, empty states, and contextual hints

The profile does **not** change:

- backend capabilities
- API endpoints
- database semantics outside the new setting
- access to advanced features

This is a shared-core, profile-driven UX approach rather than separate app modes.

## User Profiles

### Personal Proxy

Primary operator:

- self-hosting user managing personal devices, family, or a small private group

Primary concerns:

- node availability
- subscriptions
- client lifecycle
- quotas and traffic
- endpoint correctness

UI emphasis:

- proxy overview first
- nodes, clients, subscriptions, traffic, apply, diagnostics as primary
- system tools de-emphasized into secondary or advanced groupings

### VPS Ops

Primary operator:

- host administrator mainly using ZenithPanel as a lightweight VPS operations surface

Primary concerns:

- monitoring
- terminal
- files
- Docker
- firewall
- cron

UI emphasis:

- system overview first
- VPS management features primary
- proxy features retained but secondary

### Mixed

Primary operator:

- user who actively uses both sides of the product

Primary concerns:

- balanced visibility across proxy and system operations

UI emphasis:

- combined overview
- balanced section order
- current product behavior used as the baseline fallback

## Information Architecture

### Personal Proxy

Default landing page:

- proxy overview

Primary navigation:

- Proxy Overview
- Inbound Nodes
- Clients & Subs
- Traffic
- Apply & Diagnostics

Secondary navigation:

- Routing Rules
- TLS / Certificates
- Firewall

Advanced group:

- Terminal
- File Manager
- Docker
- Cron
- System tuning

### VPS Ops

Default landing page:

- system overview

Primary navigation:

- Monitoring
- Terminal
- File Manager
- Docker
- Firewall
- Cron

Secondary navigation:

- Proxy Overview
- Inbound Nodes
- Routing Rules
- TLS / Certificates

Advanced group:

- client/subscription details
- proxy lifecycle tools that are not needed day to day

### Mixed

Default landing page:

- combined overview

Primary navigation:

- Monitoring
- Proxy Overview
- Docker
- Firewall
- Clients & Subs

Secondary navigation:

- Terminal
- File Manager
- Routing Rules
- TLS / Certificates
- Cron

## Settings Model

Add a persisted setting:

- key: `usage_profile`
- values: `personal_proxy`, `vps_ops`, `mixed`

Behavior:

- selected during setup wizard
- editable later from Settings / Preferences
- applied immediately after save
- stored globally for the panel

Optional follow-up settings:

- `default_home_section`
- `pinned_sections`
- `expand_advanced_sections`

These follow-ups are not required for phase 1.

## Dashboard Behavior

### Personal Proxy Dashboard

Cards:

- engine status
- active inbounds
- active clients
- traffic today
- quota alerts
- last apply result

Quick actions:

- quick setup
- add client
- copy subscription
- apply config
- test endpoint

Warnings:

- public host missing
- port conflict
- unsupported engine/protocol pair
- apply failure

### VPS Ops Dashboard

Cards:

- CPU
- memory
- disk
- network
- Docker status
- firewall state
- cron status

Quick actions:

- open terminal
- browse files
- restart container
- add firewall rule
- run cleanup

Warnings:

- low disk
- failed jobs
- unsafe exposure

### Mixed Dashboard

Cards:

- smaller balanced set from both domains

Quick actions:

- limited but mixed set from proxy and system groups

## Setup Wizard Behavior

Add a profile selection step early in setup.

Profile-dependent defaults:

- `personal_proxy`: lead into proxy quick setup, first client creation, public host explanation
- `vps_ops`: lead into security, panel access, Docker, firewall, and monitoring
- `mixed`: use a balanced onboarding path

The setup wizard should explain that the profile changes emphasis, not feature availability.

## UX Rules

1. Profiles may reorder and de-emphasize sections.
2. Profiles must not hard-disable core features in phase 1.
3. Advanced features remain reachable through navigation or explicit links.
4. Copy, hints, empty states, and recommendations should match the chosen profile.
5. The system must degrade safely to `mixed` if the setting is absent or invalid.

## Data and API Impact

Backend impact:

- add support for reading/writing `usage_profile` through existing settings mechanisms

Frontend impact:

- central profile store/composable
- profile-aware navigation config
- profile-aware dashboard layout selection
- setup wizard profile step

API impact:

- no new dedicated domain API required if settings endpoints already cover read/write of settings
- if current settings endpoints are too fragmented, add a small preferences endpoint rather than scattering profile reads across views

## Error Handling

- if profile fetch fails, fall back to `mixed`
- if setting value is unknown, normalize to `mixed`
- if user changes profile and some view configuration is unavailable, fall back to shared default ordering

## Testing Strategy

Backend:

- settings persistence for `usage_profile`
- normalization of invalid values to `mixed`

Frontend:

- navigation order per profile
- dashboard section selection per profile
- setup wizard default path per profile
- fallback behavior when setting is missing

Integration:

- change profile in settings and confirm dashboard/navigation update without data loss

## Rollout Plan

### Phase 1

- persist `usage_profile`
- setup wizard profile selection
- profile-aware sidebar ordering
- profile-aware default home routing
- profile-aware dashboard composition

### Phase 2

- profile-aware empty states, recommendations, and quick actions
- profile-specific onboarding hints

### Phase 3

- optional personalization on top of profile:
  - pinned sections
  - default home override
  - advanced expansion preference

## Non-Goals

- separate products or builds per profile
- separate backend modes
- profile-specific permission systems
- hiding advanced functionality so completely that it becomes unreachable

## Recommendation

Implement the shared-core, profile-driven UX model.

It gives ZenithPanel clearer focus for different operator intents while keeping the open-source maintenance burden low and preserving a single coherent backend.
