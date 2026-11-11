# garret design

This directory describes how garret is built and why. It is the reference
for implementation work until the first release.

## Goal

garret is a single-binary server that lets a small team on the same access
point push pre-release changes as branches, then review and discuss them
commit by commit. Every record is kept traceable, and the whole project can be
erased when the work is done.

## Documents

| Document | Contents |
|---|---|
| [decisions.md](decisions.md) | The design decisions, their reasons and the rejected alternatives |
| [architecture.md](architecture.md) | Components, request flow, Git handling and real-time collaboration |
| [data-model.md](data-model.md) | Database schema, revision and comment model, retention rules |
| [api.md](api.md) | HTTP, WebSocket and Git endpoints |
| [security.md](security.md) | Threat model, network boundary, authentication, TLS and data handling |
| [testing.md](testing.md) | Definition of done for the first release and how it is verified |

How code is written against this design is described in
[../development-process.md](../development-process.md).

## Status

These documents describe the first release. Items marked **proposed default**
are values chosen for implementation that can still change without revisiting
a design decision.
