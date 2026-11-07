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
    layout/window_toolbar.dart      Page title, back/forward and sidebar controls
    window/desktop_window.dart       Desktop bootstrap, drag regions and window buttons
    widgets/section.dart            Shared page layout, panel headings, surfaces and badges
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
  a fixed Settings destination at the bottom. Navigation controls stay at the
  left while the page heading aligns with the expanded main content.
- Smaller or shorter windows: vertically stacked content. Desktop platforms use
  the toolbar's overlay sidebar without a duplicate bottom navigation bar;
  Android and iOS retain bottom navigation for all four sections.
  The same toolbar toggle opens and closes an overlay sidebar below the header;
  selecting a section, pressing Escape or tapping the scrim also closes it.
- The request/response area uses two columns only when its own available width
  reaches 720; it does not assume a particular device or platform.

The sidebar's bottom Settings destination opens a page inside the main content
card, with the same Back/Forward history as other sections. It currently offers
System, Light and Dark appearance through preview cards and radio choices. Both
the preview and its radio row apply the choice immediately. The standard
`RadioGroup` retains keyboard selection, while decorative previews stay out of
the accessibility tree. Cards appear side by side when space allows and stack
in narrow or enlarged-text layouts. Narrow windows expose
Settings in the overlay sidebar; Android and iOS also expose it in bottom
navigation. System is the default
and follows changes to the device appearance.
At widths of at least 600 logical pixels, the toolbar also offers a one-click
light/dark toggle. It chooses the opposite of the effective appearance, including
when System is active; Settings can restore System mode. Switching appearance
retains the current request, response and connection.
The explicit appearance and sidebar choices last for the current process;
applications own any preference persistence. Theme colors, field styles, buttons and Linux font
fallbacks are defined in `AppTheme`. Form fields share one decoration density so
dropdown and text-input borders align on desktop and grow with enlarged text.
The sidebar uses standard Flutter buttons
with selected semantics, tooltips, keyboard activation and a visible focus
outline. The sidebar has no backend status footer or account placeholder.
Runtime retains backend health details, while Workspace still reports request
and connection failures. The navigation list scrolls independently
when the available height or enlarged text requires more room.
When vertical space allows, the sidebar starts with the application name and a
compact terminal mark. Collapsed navigation retains the mark and icon tooltips.

The top toolbar contains Back, Forward and the sidebar toggle in that order,
followed by a short divider and current page title on the same row.
The application name is
retained as window metadata rather than repeated in the toolbar. The
header shares the sidebar background. The header's bottom and sidebar's right
edges have no separator lines. The row is 56 logical pixels high on macOS and
60 on other platforms.
Toolbar content is vertically centered in that row, with the content card
starting directly below it. macOS sizes the native titlebar to the same height
so the window buttons remain fully inside their containers, aligns their
centers with the row and restores the alignment when AppKit repositions the buttons or
their containers, including after window activation, updates, resizing or leaving
fullscreen. macOS reserves the leading 88 logical
pixels for native window controls. Navigation buttons stay beside those controls
at every sidebar state; other platforms use normal leading spacing. Expanded
desktop navigation aligns the page divider with the main content's leading edge,
while collapsed and overlay navigation place it directly after the buttons.
The page heading's 220-millisecond transition follows the sidebar's curve and
honors reduced motion. Switching to overlay navigation resets only the heading
inset so narrow windows retain all controls. The toolbar has no separate brand
icon. Back and Forward
follow local section visits, disable at history boundaries, and keep input and
responses alive. Revisiting the current section adds no entry; selecting a new
section after going back clears the forward branch. History keeps the most
recent 100 visits for the current process and does not implement URL routing.

Sidebar width and labels transition over 220 milliseconds. The layout clips
intermediate content. It honors
the platform's reduce-motion setting with immediate transitions. Workspace
introduces the request playground with a connection badge derived from the live
view model. Its request panel includes a select-only example dropdown with
Flutter, Bridra and custom input choices. Selecting an example updates the actual
name and JSON payload; choosing custom input preserves the draft. The dropdown
uses the shared neutral field and menu themes, supports keyboard navigation and
Escape dismissal, and stacks above the name field on narrow or enlarged-text
layouts. The panel shows an escaped JSON preview of the current input;
the response panel presents waiting, progress, failure and success states with
the actual middleware pipeline. Narrow layouts stack the panels vertically.

Runtime, Guide and Settings use `FeaturePage` for the same responsive page
padding and heading hierarchy. All four features share `SectionHeading` for
panel titles, descriptions and icons. Runtime presents its live connection
status, transport, framework version and RPC protocol above the reported
application pipeline; disconnected fields use waiting or unavailable values.
Guide groups selectable commands into bordered code surfaces with copy actions
and places the application directory map beside them on wide layouts. Settings
groups the appearance previews in a bordered panel and explains that the choice
lasts for the current session. These layouts keep the existing section history,
scroll positions and shared backend connection.

The main content fills one inset card with 12-pixel rounded corners and clipped
scrolling content. The surrounding shell shares the sidebar background; feature
panels remain inside the card. The shadcn-inspired appearance uses a neutral zinc
palette, explicit typography, thin borders, 6-pixel control corners and 10-pixel
panel corners, without adding a UI package. Light mode pairs a near-white shell
(`#FAFAFA`) with a white canvas (`#FFFFFF`); dark mode pairs a near-black shell
(`#101012`) with a deep zinc canvas (`#09090B`). Primary actions use inverse
foreground/background colors, while connection and response states use semantic
colors. On macOS, the starter extends content into a transparent
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
uses the platform's normal close request. The page-title area supports
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
badge. Settings shares the main content card's bottom inset and keeps an 8-pixel
horizontal inset. Expanded sidebars
show its icon and label; compact sidebars show its icon with a tooltip.
A temporary narrow window uses compact or overlay navigation, with bottom
navigation retained on Android and iOS, without resetting
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
