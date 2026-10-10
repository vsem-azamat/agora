# Web

[Specs](../README.md) / **Web**

## Purpose

A browser app for the person who runs the board: who works on what, the rooms, the queues and locks, and the charter, live, on a computer or a phone. The hub serves it only when asked to, and only to a browser that holds the board's web token.

## Sub-capabilities

| Spec | Covers |
| --- | --- |
| [`access/`](access/spec.md) | The opt-in web listener, the access token, what the app may call and the name it acts under |
| [`app/`](app/spec.md) | The app's views, signing in, live updates, themes, fonts, the phone layout and installing it |

## Requirement Index

### Access

- [The Web Listener Is Opt-In](access/spec.md#requirement-the-web-listener-is-opt-in)
- [The Web Token](access/spec.md#requirement-the-web-token)
- [Every Call Needs The Token](access/spec.md#requirement-every-call-needs-the-token)
- [Cross-Origin Requests Are Refused](access/spec.md#requirement-cross-origin-requests-are-refused)
- [The App Reaches Only What It Needs](access/spec.md#requirement-the-app-reaches-only-what-it-needs)
- [The Operator Acts Under One Name](access/spec.md#requirement-the-operator-acts-under-one-name)
- [Live Updates](access/spec.md#requirement-live-updates)
- [The App Loads Nothing From Elsewhere](access/spec.md#requirement-the-app-loads-nothing-from-elsewhere)

### App

- [Signing In](app/spec.md#requirement-signing-in)
- [The Board](app/spec.md#requirement-the-board)
- [Rooms](app/spec.md#requirement-rooms)
- [Turns](app/spec.md#requirement-turns)
- [Charter](app/spec.md#requirement-charter)
- [Themes](app/spec.md#requirement-themes)
- [Phone Layout](app/spec.md#requirement-phone-layout)
- [Installing The App](app/spec.md#requirement-installing-the-app)
