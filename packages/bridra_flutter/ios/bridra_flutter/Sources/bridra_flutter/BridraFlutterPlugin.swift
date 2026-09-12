import Flutter
import Foundation

/// Narrow Swift contract implemented by an application's gomobile wrapper.
///
/// The application owns construction, credentials, persistence, and the
/// XCFramework that backs this interface.
public protocol BridraEmbeddedRuntime: AnyObject {
    func callJSON(_ requestJSON: String) throws -> String
    func cancel(_ requestID: String) -> Bool
    func close(timeoutMilliseconds: Int64) throws
}

public enum BridraEmbeddedRuntimeInstallationError: Error {
    case alreadyInstalled
}

public final class BridraFlutterPlugin: NSObject, FlutterPlugin {
    private static let runtimeStore = RuntimeStore()

    private let channel: FlutterMethodChannel
    private let queue = DispatchQueue(
        label: "dev.cluion.bridra.embedded-rpc",
        qos: .userInitiated,
        attributes: .concurrent
    )

    public static func register(with registrar: FlutterPluginRegistrar) {
        let channel = FlutterMethodChannel(
            name: "dev.cluion.bridra/embedded_rpc",
            binaryMessenger: registrar.messenger()
        )
        let plugin = BridraFlutterPlugin(channel: channel)
        registrar.addMethodCallDelegate(plugin, channel: channel)
        registrar.publish(plugin)
    }

    /// Installs the application-owned runtime used by subsequent channel calls.
    /// Replacing a live runtime is refused so ownership cannot become ambiguous.
    public static func installEmbeddedRuntime(
        _ runtime: any BridraEmbeddedRuntime
    ) throws {
        try runtimeStore.install(runtime)
    }

    init(channel: FlutterMethodChannel) {
        self.channel = channel
        super.init()
    }

    public func handle(_ call: FlutterMethodCall, result: @escaping FlutterResult) {
        switch call.method {
        case "call":
            handleCall(call.arguments, result: result)
        case "cancel":
            handleCancel(call.arguments, result: result)
        case "close":
            handleClose(call.arguments, result: result)
        default:
            result(FlutterMethodNotImplemented)
        }
    }

    private func handleCall(_ arguments: Any?, result: @escaping FlutterResult) {
        guard
            let values = arguments as? [String: Any],
            let requestJSON = values["requestJSON"] as? String
        else {
            result(channelError("invalid_arguments", "call requires requestJSON."))
            return
        }
        guard let runtime = Self.runtimeStore.current() else {
            result(channelError("runtime_unavailable", "No embedded runtime is installed."))
            return
        }
        queue.async {
            do {
                let response = try runtime.callJSON(requestJSON)
                Self.complete(result, value: response)
            } catch {
                Self.complete(result, value: self.runtimeError(error))
            }
        }
    }

    private func handleCancel(_ arguments: Any?, result: @escaping FlutterResult) {
        guard
            let values = arguments as? [String: Any],
            let requestID = values["requestID"] as? String,
            !requestID.isEmpty
        else {
            result(channelError("invalid_arguments", "cancel requires a non-empty requestID."))
            return
        }
        guard let runtime = Self.runtimeStore.current() else {
            result(channelError("runtime_unavailable", "No embedded runtime is installed."))
            return
        }
        queue.async {
            Self.complete(result, value: runtime.cancel(requestID))
        }
    }

    private func handleClose(_ arguments: Any?, result: @escaping FlutterResult) {
        guard
            let values = arguments as? [String: Any],
            let milliseconds = values["timeoutMilliseconds"] as? NSNumber,
            milliseconds.int64Value > 0
        else {
            result(channelError("invalid_arguments", "close requires a positive timeoutMilliseconds."))
            return
        }
        guard let runtime = Self.runtimeStore.beginClose() else {
            result(nil)
            return
        }
        queue.async {
            do {
                try runtime.close(timeoutMilliseconds: milliseconds.int64Value)
                Self.runtimeStore.finishClose(succeeded: true)
                Self.complete(result, value: nil)
            } catch {
                Self.runtimeStore.finishClose(succeeded: false)
                Self.complete(result, value: self.runtimeError(error))
            }
        }
    }

    private func runtimeError(_ error: Error) -> FlutterError {
        channelError(
            "runtime_error",
            "The embedded runtime operation failed.",
            details: String(reflecting: type(of: error))
        )
    }

    private func channelError(
        _ code: String,
        _ message: String,
        details: Any? = nil
    ) -> FlutterError {
        FlutterError(code: code, message: message, details: details)
    }

    private static func complete(_ result: @escaping FlutterResult, value: Any?) {
        DispatchQueue.main.async {
            result(value)
        }
    }
}

private final class RuntimeStore {
    private let lock = NSLock()
    private var runtime: (any BridraEmbeddedRuntime)?
    private var closing = false

    func install(_ runtime: any BridraEmbeddedRuntime) throws {
        lock.lock()
        defer { lock.unlock() }
        guard self.runtime == nil else {
            throw BridraEmbeddedRuntimeInstallationError.alreadyInstalled
        }
        self.runtime = runtime
        closing = false
    }

    func current() -> (any BridraEmbeddedRuntime)? {
        lock.lock()
        defer { lock.unlock() }
        return closing ? nil : runtime
    }

    func beginClose() -> (any BridraEmbeddedRuntime)? {
        lock.lock()
        defer { lock.unlock() }
        guard let runtime else {
            return nil
        }
        closing = true
        return runtime
    }

    func finishClose(succeeded: Bool) {
        lock.lock()
        defer { lock.unlock() }
        if succeeded {
            runtime = nil
            closing = false
        }
    }
}
