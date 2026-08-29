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
  private var applicationLifecycleChannel: FlutterMethodChannel?
  private var applicationLifecycleHandler: BridraApplicationLifecycleHandler?
  private var resourceBookmarkChannel: FlutterMethodChannel?
  private var resourceBookmarkHandler: BridraResourceBookmarkHandler?

  override func awakeFromNib() {
    let flutterViewController = FlutterViewController()
    let windowFrame = self.frame
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
  }
}
