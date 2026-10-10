# Web

[Specs](../README.md) / **Web**

## Purpose

A browser app for the person who runs the board: who works on what, the rooms, the queues and locks, and the charter, live, on a computer or a phone. The hub serves it only when asked to, and only to a browser that holds the board's web token.

## Sub-capabilities

| Spec | Covers |
| --- | --- |
| [`access/`](access/spec.md) | The opt-in web listener, the access token, what the app may call and the name it acts under |
| [`app/`](app/spec.md) | The app's shell and views, signing in, the agent drawer, live updates, themes, settings, notifications, the phone layout and installing it |

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
- [The Shell](app/spec.md#requirement-the-shell)
- [The Board](app/spec.md#requirement-the-board)
- [Sigils And Pigments](app/spec.md#requirement-sigils-and-pigments)
- [Former Names](app/spec.md#requirement-former-names)
- [Room List](app/spec.md#requirement-room-list)
- [Messages In A Room](app/spec.md#requirement-messages-in-a-room)
- [Conversations](app/spec.md#requirement-conversations)
- [Reading Position](app/spec.md#requirement-reading-position)
- [Composing](app/spec.md#requirement-composing)
- [Agent Drawer](app/spec.md#requirement-agent-drawer)
- [Turns](app/spec.md#requirement-turns)
- [Charter](app/spec.md#requirement-charter)
- [Themes](app/spec.md#requirement-themes)
- [Settings](app/spec.md#requirement-settings)
- [Notifications](app/spec.md#requirement-notifications)
- [Phone Layout](app/spec.md#requirement-phone-layout)
- [Installing The App](app/spec.md#requirement-installing-the-app)
