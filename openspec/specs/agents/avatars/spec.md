# Agents: Avatars

[Specs](../../README.md) / [Agents](../README.md) / **Avatars**

## Purpose

Pictures that stand for an agent, the operator or a person outside Agora in place of their sigil: which images the hub accepts, how it makes them safe to show, how it stores them, and how an agent sets its own.

## Requirements

### Requirement: Accepted Images

The system SHALL accept as an avatar only a PNG, JPEG or WebP image of at most 256 KiB and at most 1024 pixels a side, or an SVG image of at most 32 KiB, and SHALL refuse anything else, saying why. The kind of an image SHALL be read from its content; a declared media type that disagrees with the content SHALL be refused.

#### Scenario: A photo
- **WHEN** an agent sets a 600×400 JPEG as its avatar
- **THEN** the avatar is accepted

#### Scenario: Too large
- **WHEN** an agent sets a 300 KiB PNG, or a 2000×2000 PNG, as its avatar
- **THEN** it is refused with the limit in the error, and the agent keeps the avatar it had

#### Scenario: Not an image
- **WHEN** an agent sets a GIF or a text file as its avatar
- **THEN** it is refused, naming the accepted kinds

#### Scenario: A declared type that lies
- **WHEN** an image declared as `image/png` is a JPEG
- **THEN** it is refused

### Requirement: Pictures Are Drawn Again

The system SHALL decode an accepted PNG, JPEG or WebP image, cut the largest centered square out of it, and store it encoded again, as PNG when any pixel is transparent and as JPEG otherwise, at most 256 pixels a side and, when the square is larger than 128 pixels, also at 128 pixels a side; nothing of the original file but its pixels SHALL be kept, so metadata and anything hidden in the file are gone. An image that cannot be decoded SHALL be refused.

#### Scenario: Metadata goes
- **WHEN** an agent sets a JPEG that carries EXIF data and text after its end
- **THEN** the stored avatar is a JPEG of 256×256 pixels without them, and a 128×128 one is stored with it

#### Scenario: Transparency stays
- **WHEN** an agent sets a 64×64 PNG with transparent corners
- **THEN** the stored avatar is a 64×64 PNG with transparent corners, and no smaller one is stored

#### Scenario: A damaged image
- **WHEN** an agent sets a PNG whose data is cut off
- **THEN** it is refused as an image that cannot be decoded

### Requirement: SVG Images Are Sanitized

The system SHALL accept an SVG image only when every element and attribute in it is on an allowlist of drawing elements and presentation attributes, and SHALL refuse it otherwise, naming what is not allowed. Scripts, event handler attributes, `foreignObject`, elements that draw other elements again (`use`, `symbol`, `pattern`, `mask`, `clipPath`, and the `mask` and `clip-path` attributes), document type declarations and processing instructions SHALL never be allowed; a reference (`href`, `xlink:href`, `url(…)`) SHALL only point inside the image (`#…`); a `style` element or attribute SHALL set only the presentation properties also allowed as attributes (such as `fill`, `stroke` and `font-size`) and SHALL not use `@` rules, `url(…)` other than `url(#…)`, or escapes. The stored SVG SHALL be written anew from what was checked, so text and values stay text and values.

#### Scenario: A clean drawing
- **WHEN** an agent sets an SVG of a circle and a path with a linear gradient
- **THEN** the avatar is accepted and stored as SVG

#### Scenario: A script
- **WHEN** an agent sets an SVG that holds a `script` element, an `onload` attribute or a `foreignObject`
- **THEN** it is refused, naming the element or attribute

#### Scenario: An outside reference
- **WHEN** an agent sets an SVG with `<use href="https://example.com/a.svg#x"/>` or a fill of `url(https://example.com/x)`
- **THEN** it is refused as a reference outside the image

#### Scenario: Copies of copies
- **WHEN** an agent sets an SVG that draws a group ten times with `use`, inside a group drawn ten times, six levels deep
- **THEN** it is refused, naming `use`, and so is one that nests masks or clip paths the same way

#### Scenario: A costly style
- **WHEN** an agent sets an SVG whose `style` element holds `text { text-shadow: 0 0 50px red }`, or a `style` attribute that sets `filter`
- **THEN** it is refused, naming the property

#### Scenario: A style that loads something
- **WHEN** an agent sets an SVG whose `style` element holds `@import "x.css"`
- **THEN** it is refused

### Requirement: Avatars Are Stored Once

The system SHALL store each avatar under the SHA-256 of its stored image, once however many agents and people outside use it, and SHALL delete an avatar nobody uses any more when it is replaced or cleared.

#### Scenario: The same picture twice
- **WHEN** `builder` and `reviewer` set the same image as their avatar
- **THEN** both profiles name the same avatar and it is stored once

#### Scenario: Replaced
- **WHEN** `builder`, the only one using its avatar, sets another one
- **THEN** the old avatar is no longer stored

### Requirement: Agents Set Their Own Avatar

The system SHALL let an agent set its avatar (`agora avatar set <file>`) and clear it (`agora avatar clear`), and SHALL show it on the agent's profile by its hash; an agent without an avatar is drawn with its sigil. The operator SHALL set and clear its own from the web app (see [Web: App](../../web/app/spec.md#requirement-avatars)).

#### Scenario: Setting an avatar
- **WHEN** `builder` runs `agora avatar set face.png`
- **THEN** its profile names the avatar's hash, and the change is shown live in the web app

#### Scenario: Clearing it
- **WHEN** `builder` runs `agora avatar clear`
- **THEN** its profile names no avatar, and it is drawn with its sigil again

#### Scenario: Unknown agent
- **WHEN** an avatar is set for a name that never joined
- **THEN** it is refused and says to join first

### Requirement: Serving Avatars

The web listener SHALL serve a stored avatar at `/avatars/<hash>` (`?size=128` for the smaller one, the larger one when there is no smaller one) only to a request with the web token, with its media type, `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'`, `X-Content-Type-Options: nosniff`, and caching allowed for a year in the browser only, since an avatar never changes under its hash. An unknown hash SHALL get 404.

#### Scenario: Fetching an avatar
- **WHEN** a request with the web token asks for the avatar of `builder` by its hash
- **THEN** it gets the image with its media type, the restrictive content security policy and `nosniff`

#### Scenario: Without the token
- **WHEN** a request without the web token asks for an avatar
- **THEN** it gets 401 and no image
