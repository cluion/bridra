# Starter UI architecture

The reference application and generated projects use a small, application-owned
Flutter UI. Workspace provides a live request playground, Runtime shows backend
health, Guide explains the project structure and development commands, and
Settings owns the appearance controls.

## Responsibilities

```text
lib/
  main.dart                         Platform bootstrap and single-instance ownership
  app/starter_app.dart              Composition, section history and preferences
  ui/
    theme/app_theme.dart            Light/dark themes and Linux CJK fallback
    layout/app_shell.dart           Adaptive sidebar and section navigation
    layout/window_toolbar.dart      Product/page titles, back/forward and sidebar controls
    window/desktop_window.dart       Desktop bootstrap, drag regions and window buttons
    widgets/section.dart            Shared panel surfaces
    widgets/app_toast.dart          Floating, nonblocking copy confirmations
  features/
    workspace/
      workspace_view.dart           Request form and response presentation
      workspace_view_model.dart     Connection/request state and gateway ownership
    runtime/runtime_view.dart       Live health presentation
    guide/guide_view.dart           Project guidance and command copying
    settings/settings_view.dart     System, Light and Dark appearance controls
  api/
    backend_gateway.dart            Application gateway and transport composition
    generated/bridra_api.g.dart     Generated RPC types and methods
```

`StarterApp` creates one `WorkspaceViewModel` and injects the connector. The
view model exposes read-only presentation state through Flutter's
`ChangeNotifier`; views send commands and observe updates. Runtime reads the
same workspace connection rather than opening another backend. The gateway
remains the boundary for typed RPC calls. Go Controllers and Services continue
to own application behavior.

Text editing belongs to the Workspace view. Navigation history, theme mode
and the sidebar's collapsed preference belong to the app. Changing them keeps
the connection and the request draft.
`IndexedStack` keeps feature views mounted, and each scroll view has a stable
storage key. Replacing an injected connector disposes the previous view model.

The view model prevents overlapping connection attempts and requests, clears a
previous response when a new request starts, and distinguishes request errors
from transport loss. Transport loss detaches the gateway and enables reconnect.
Disposal cancels an active request and closes the owned gateway. A connection
that completes after disposal is closed without notifying a disposed view.

This follows Flutter's [view/view-model guidance](https://docs.flutter.dev/app-architecture/guide)
using the SDK's built-in observables. Applications can replace this starter
pattern when their own requirements call for another state-management approach.

## Layout and appearance

The shell uses the available window size:

- At least 760 logical pixels wide and 480 high: a 64-pixel icon sidebar by
  default. The toolbar toggle expands it to 240 pixels at any desktop width.
- At least 1100 wide: a 240-pixel sidebar with full-row navigation selection and
  a fixed Settings destination at the bottom. The top toolbar remains fixed
  while the sidebar animates between 240 and 64 pixels.
- Smaller or shorter windows: bottom navigation for all four sections and
  vertically stacked content.
  The same toolbar toggle opens and closes an overlay sidebar below the header;
  selecting a section, pressing Escape or tapping the scrim also closes it.
- The request/response area uses two columns only when its own available width
  reaches 720; it does not assume a particular device or platform.

The sidebar's bottom Settings destination opens a page inside the main content
card, with the same Back/Forward history as other sections. It currently offers
System, Light and Dark appearance through radio choices. Narrow windows expose
Settings in bottom navigation and in the overlay sidebar. System is the default
and follows changes to the device appearance.
The explicit appearance and sidebar choices last for the current process;
applications own any preference persistence. Theme colors, field styles, buttons and Linux font
fallbacks are defined in `AppTheme`. The sidebar uses standard Flutter buttons
with selected semantics, tooltips, keyboard activation and a visible focus
outline. The sidebar has no backend status footer or account placeholder.
Runtime retains backend health details, while Workspace still reports request
and connection failures. The navigation list scrolls independently
when the available height or enlarged text requires more room.

The top toolbar contains the application name, Back, Forward and the sidebar
toggle in that order, followed by the current page title on the same row. The
header shares the sidebar background. The header and sidebar have no separator
lines. The row is 56 logical pixels high on macOS and 60 on other platforms.
Toolbar content is vertically centered in that row, with the content card
starting directly below it. macOS sizes the native titlebar to the same height
so the window buttons remain fully inside their containers, aligns their
centers with the row and restores the alignment when AppKit repositions the buttons or
their containers, including after window activation, updates, resizing or leaving
fullscreen. macOS reserves the leading 88 logical
pixels for native window controls, placing the name beside the green button; other platforms
use normal leading spacing. There is no separate brand icon. Back and Forward
follow local section visits, disable at history boundaries, and keep input and
responses alive. Revisiting the current section adds no entry; selecting a new
section after going back clears the forward branch. History keeps the most
recent 100 visits for the current process and does not implement URL routing.

Sidebar width and labels transition over 220 milliseconds. The layout clips
intermediate content. It honors
the platform's reduce-motion setting with immediate transitions. Workspace
starts directly with its request/response panels, without a repeated page heading.

The main content fills one inset card with 20-pixel rounded corners and clipped
scrolling content. The surrounding shell shares the sidebar background; feature
panels remain inside the card. The light appearance uses a pale-gray shell
(`#EAEAEB`) and a white main canvas (`#FFFFFF`), with neutral-gray feature panels,
fields, selection states and borders. The dark appearance uses a charcoal shell
(`#27292B`) and a solid deep-gray main canvas (`#181818`). Feature panels, fields
and selection states use neutral gray shades, with blue accents for primary
actions and muted panel borders. Translucent in-app panels are composited over
the Flutter background. On macOS, the starter extends content into a transparent
title bar and hides the window title and separator. Native close, minimize,
zoom/fullscreen controls and the title-bar drag region remain platform-owned.
The toolbar reserves space beside those controls. This uses AppKit's
[full-size content view](https://developer.apple.com/documentation/appkit/nswindow/stylemask-swift.struct/fullsizecontentview)
and [transparent title bar](https://developer.apple.com/documentation/appkit/nswindow/titlebarappearstransparent);
it does not enable native desktop wallpaper blur.

On Windows and Linux, the entrypoint initializes the application-owned
`desktop_window.dart` adapter before rendering. It hides the separate native
title bar and uses the existing 60-pixel toolbar for minimize, maximize/restore
and close controls on the right. The buttons have tooltips, semantic labels and
keyboard focus. Native window events update the maximize/restore state; closing
uses the platform's normal close request. Product and page-title areas support
dragging and double-click maximize/restore, while navigation buttons keep their
own interactions. Resize edges remain available through the virtual window
frame. The minimum native window size is 320 by 240 logical pixels, and narrow
headers shorten their titles while keeping navigation and window buttons usable.

This adapter uses [window_manager](https://pub.dev/packages/window_manager),
separate from `bridra_flutter` and its RPC contracts. Generated projects include
the plugin and desktop-window regressions only when Windows or Linux is selected.
Other platform selections receive a lightweight adapter with the same interface.
`StarterApp.desktopWindowControls` defaults to false for direct widget embedding;
the generated entrypoint enables it after successful desktop initialization.
macOS continues to use its existing AppKit controls and alignment.
Windows development needs Flutter's native-plugin symbolic-link support, through
Windows Developer Mode or an elevated development session.
Backend health details live in Runtime; the toolbar and sidebar have no status
badge. Settings sits near the bottom with an 8-pixel inset. Expanded sidebars
show its icon and label; compact sidebars show its icon with a tooltip.
A temporary narrow window uses compact or bottom navigation without resetting
the user's collapsed preference. The toolbar toggle remains
visible and usable at every size. Open overlays close when returning to a desktop
layout. Very short overlays scroll all navigation, including Settings, together.
See Flutter's [adaptive layout guidance](https://docs.flutter.dev/ui/adaptive-responsive/general)
for application-specific navigation choices.

Starter navigation is local section selection. Applications needing URL routes,
deep links, browser history or nested navigation should add an application-owned
router at the composition boundary.

Copy confirmations use `AppToastHost.show(context, message)` from feature views.
The shell owns one toast host below the toolbar: a centered card slides down and
fades in, then dismisses after three seconds or through its close button. Repeated
copies replace the current message and restart its expiry rather than queueing.
The toast uses the active theme, adapts to narrow windows and enlarged text,
announces through a semantic live region, and leaves focus and underlying
controls available. Reduced motion makes entry and dismissal immediate. The host
cancels its expiry timer when disposed; it is an application-owned UI widget.

## Adding a feature

1. Add a feature directory containing its view and presentation state.
2. Inject required gateways or services from `app/`; keep transport creation out
   of widgets and use the generated typed API.
3. Add an appropriate destination or application route. Keep state scoped to
   the feature or shared connection that actually owns it.
4. Test asynchronous state/lifetime behavior separately from widget layout,
   then verify the affected navigation and narrow-window interaction.

The reference application keeps `BridraApp` as a small entry wrapper. Generated
projects export `StarterApp` from `main.dart` for widget/integration tests.
Maintainers edit the reference UI and tests, then run
`python3 tool/sync_starter_ui.py` to update the embedded Dart templates. Use
`--check` for a read-only drift check. This tool only writes the repository's
known embedded templates; it never changes generated consumer projects.
The project-template parity regression keeps the twelve shared UI modules and
macOS window bootstrap and native window regressions in the reference application
and generated starter aligned. The native alignment regression checks the actual
displayed application window after launch, native button/container repositioning,
AppKit layout and native zoom/restore. It also checks that the buttons remain
inside their parents and visible. It runs with the existing
`make macos-native-test` target.

## Existing applications

This UI is an optional starter. Dependency upgrades do not rename directories,
replace screens or overwrite application-owned UI. UI-only adoption does not
change the existing template/runtime protocol contracts.

Compare a disposable newly generated project with your application, adopt the
parts you need, and retain your existing single-instance ID, platform lifecycle,
gateway customization and domain behavior. Template rendering preserves existing
UI and starter test files marked as application-owned. See
[UPGRADING.md](UPGRADING.md) for dependency and required-runtime migration rules.
