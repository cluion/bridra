import Cocoa
import FlutterMacOS

final class BridraApplicationLifecycleHandler {
  static let channelName = "dev.cluion.bridra/application"

  private let terminateApplication: () -> Void

  init(terminateApplication: @escaping () -> Void = { NSApp.terminate(nil) }) {
    self.terminateApplication = terminateApplication
  }

  func handle(_ call: FlutterMethodCall, result: @escaping FlutterResult) {
    guard call.method == "terminateSecondary" else {
      result(FlutterMethodNotImplemented)
      return
    }

    result(nil)
    terminateApplication()
  }
}

final class BridraResourceBookmarkHandler {
  static let channelName = "dev.cluion.bridra/resources"
  static let maximumBookmarkBytes = 1 << 20

  typealias BookmarkCreator = (URL, URL.BookmarkCreationOptions) throws -> Data

  private let createBookmark: BookmarkCreator

  init(
    createBookmark: @escaping BookmarkCreator = { url, options in
      try url.bookmarkData(
        options: options,
        includingResourceValuesForKeys: nil,
        relativeTo: nil
      )
    }
  ) {
    self.createBookmark = createBookmark
  }

  func handle(_ call: FlutterMethodCall, result: @escaping FlutterResult) {
    guard call.method == "createBookmark" else {
      result(FlutterMethodNotImplemented)
      return
    }
    guard
      let arguments = call.arguments as? [String: Any],
      Set(arguments.keys) == Set(["path", "scope", "readOnly"]),
      let path = arguments["path"] as? String,
      !path.isEmpty,
      !path.contains("\0"),
      (path as NSString).isAbsolutePath,
      let scope = arguments["scope"] as? String,
      scope == "ephemeral" || scope == "persistent",
      let readOnly = arguments["readOnly"] as? Bool
    else {
      result(Self.error(
        code: "resource_bookmark_invalid",
        message: "The resource bookmark request is invalid."
      ))
      return
    }

    var options: URL.BookmarkCreationOptions = []
    if scope == "persistent" {
      options.insert(.withSecurityScope)
      if readOnly {
        options.insert(.securityScopeAllowOnlyReadAccess)
      }
    }
    do {
      let data = try createBookmark(URL(fileURLWithPath: path), options)
      guard !data.isEmpty else {
        result(Self.error(
          code: "resource_bookmark_failed",
          message: "macOS did not create resource bookmark data."
        ))
        return
      }
      guard data.count <= Self.maximumBookmarkBytes else {
        result(Self.error(
          code: "resource_bookmark_too_large",
          message: "The resource bookmark exceeds the supported size."
        ))
        return
      }
      result(FlutterStandardTypedData(bytes: data))
    } catch {
      result(Self.error(
        code: "resource_bookmark_failed",
        message: "macOS could not create resource bookmark data."
      ))
    }
  }

  private static func error(code: String, message: String) -> FlutterError {
    FlutterError(code: code, message: message, details: nil)
  }
}

class MainFlutterWindow: NSWindow {
  private var isAligningWindowControls = false
  private var applicationLifecycleChannel: FlutterMethodChannel?
  private var applicationLifecycleHandler: BridraApplicationLifecycleHandler?
  private var resourceBookmarkChannel: FlutterMethodChannel?
  private var resourceBookmarkHandler: BridraResourceBookmarkHandler?

  override func awakeFromNib() {
    let flutterViewController = FlutterViewController()
    let windowFrame = self.frame
    styleMask.insert(.fullSizeContentView)
    titleVisibility = .hidden
    titlebarAppearsTransparent = true
    if #available(macOS 11.0, *) {
      titlebarSeparatorStyle = .none
    }
    self.contentViewController = flutterViewController
    self.setFrame(windowFrame, display: true)

    RegisterGeneratedPlugins(registry: flutterViewController)

    let lifecycleHandler = BridraApplicationLifecycleHandler()
    let lifecycleChannel = FlutterMethodChannel(
      name: BridraApplicationLifecycleHandler.channelName,
      binaryMessenger: flutterViewController.engine.binaryMessenger
    )
    lifecycleChannel.setMethodCallHandler(lifecycleHandler.handle)
    applicationLifecycleHandler = lifecycleHandler
    applicationLifecycleChannel = lifecycleChannel

    let bookmarkHandler = BridraResourceBookmarkHandler()
    let bookmarkChannel = FlutterMethodChannel(
      name: BridraResourceBookmarkHandler.channelName,
      binaryMessenger: flutterViewController.engine.binaryMessenger
    )
    bookmarkChannel.setMethodCallHandler(bookmarkHandler.handle)
    resourceBookmarkHandler = bookmarkHandler
    resourceBookmarkChannel = bookmarkChannel

    super.awakeFromNib()

    configureWindowControlAlignment()
  }

  @objc private func configureWindowControlAlignment() {
    for name in [
      NSWindow.didBecomeKeyNotification,
      NSWindow.didUpdateNotification,
      NSWindow.didResizeNotification,
      NSWindow.didExitFullScreenNotification,
    ] {
      NotificationCenter.default.addObserver(
        self,
        selector: #selector(alignWindowControls),
        name: name,
        object: self
      )
    }
    // AppKit can reposition the buttons after window notifications have fired.
    var observed = Set<ObjectIdentifier>()
    for type in [NSWindow.ButtonType.closeButton, .miniaturizeButton, .zoomButton] {
      var current: NSView? = standardWindowButton(type)
      while let view = current, observed.insert(ObjectIdentifier(view)).inserted {
        view.postsFrameChangedNotifications = true
        NotificationCenter.default.addObserver(
          self,
          selector: #selector(alignWindowControls),
          name: NSView.frameDidChangeNotification,
          object: view
        )
        current = view.superview
      }
    }
    alignWindowControls()
  }

  @objc private func alignWindowControls() {
    guard !isAligningWindowControls, !styleMask.contains(.fullScreen),
      let content = contentView else { return }
    isAligningWindowControls = true
    defer { isAligningWindowControls = false }
    let toolbarHeight: CGFloat = 56
    // Keep native button hit areas inside the titlebar when centering a taller row.
    var resizedContainers = Set<ObjectIdentifier>()
    for type in [NSWindow.ButtonType.closeButton, .miniaturizeButton, .zoomButton] {
      guard let parent = standardWindowButton(type)?.superview,
        let container = parent.superview,
        resizedContainers.insert(ObjectIdentifier(container)).inserted else { continue }
      var containerFrame = container.frame
      containerFrame.origin.y = containerFrame.maxY - toolbarHeight
      containerFrame.size.height = toolbarHeight
      if containerFrame != container.frame { container.frame = containerFrame }
      var parentFrame = parent.frame
      parentFrame.origin.y = container.bounds.minY
      parentFrame.size.height = toolbarHeight
      if parentFrame != parent.frame { parent.frame = parentFrame }
    }
    // Match the center of WindowToolbar's row using native buttons.
    let center = NSPoint(
      x: 0,
      y: content.isFlipped
        ? content.bounds.minY + toolbarHeight / 2
        : content.bounds.maxY - toolbarHeight / 2
    )
    for type in [NSWindow.ButtonType.closeButton, .miniaturizeButton, .zoomButton] {
      guard let button = standardWindowButton(type), let parent = button.superview else { continue }
      var origin = button.frame.origin
      origin.y = parent.convert(center, from: content).y - button.frame.height / 2
      if origin != button.frame.origin { button.setFrameOrigin(origin) }
    }
  }
}
