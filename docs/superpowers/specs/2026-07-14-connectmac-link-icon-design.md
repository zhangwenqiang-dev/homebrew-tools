# ConnectMac Link Icon Design

## Goal

Give ConnectMac one recognizable link mark across the Web manager, packaged resources, local-agent endpoint, and project documentation.

## Design

- Add one lightweight SVG asset at `web/assets/connectmac-mark.svg` with a blue chain-link mark and transparent background.
- Use the mark beside the Web manager title and as the page favicon.
- Use compact inline icons in action buttons: link for Connect, display for VNC, and transfer arrows for Transfer. Each button keeps its text label and tooltip.
- Package the SVG as a shared Web resource and expose the same mark from the local agent at `/icon.svg`; the local agent is a background service, not a native GUI app, so it has no dock/menu-bar icon to attach.
- Show the same mark in README using a repository-relative image link.

The icon is decorative and must not carry the only meaning of an action. Existing text, disabled states, and accessible labels remain.

## Non-Goals

- No native `.icns` application bundle is introduced for the command-line/local-agent process.
- No third-party icon dependency is added.
- No AWS behavior changes.

## Verification

- Parse the SVG as XML and verify the Web script syntax.
- Assert the Web title, favicon, action icon labels, local-agent `/icon.svg`, and README asset reference.
- Run the existing Go test suite and `go vet`.
