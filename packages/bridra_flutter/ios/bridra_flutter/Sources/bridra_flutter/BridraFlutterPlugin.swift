import Flutter
import Foundation

/// Narrow Swift contract implemented by an application's gomobile wrapper.
///
/// The application owns construction, credentials, persistence, and the
/// XCFramework that backs this interface.
public protocol BridraEmbeddedRuntime: AnyObject {
    func callJSON(_ requestJSON: String) throws -> String
    func streamJSON(_ requestJSON: String) throws -> any BridraEmbeddedStream
    func cancel(_ requestID: String) -> Bool
    func grantResourcePath(_ path: String) throws -> String
    func releaseResource(_ capability: String) throws
    func openDownload(_ fileID: String, offset: Int64) throws -> any BridraEmbeddedDownload
    func beginUploadJSON(
        _ name: String,
        mediaType: String,
        size: Int64,
        sha256: String
    ) throws -> String
    func appendUploadJSON(_ fileID: String, offset: Int64, chunk: Data) throws -> String
    func uploadStatusJSON(_ fileID: String) throws -> String
    func close(timeoutMilliseconds: Int64) throws
}

public protocol BridraEmbeddedStream: AnyObject {
    func requestID() -> String
    func nextJSON() throws -> String?
}

public protocol BridraEmbeddedDownload: AnyObject {
    func nextChunk(_ maxBytes: Int) throws -> Data?
    func close(commit: Bool) throws
}

public enum BridraEmbeddedRuntimeInstallationError: Error {
    case alreadyInstalled
}

public enum BridraSecurityScopedResourceError: Error {
    case mainThreadRequired
    case runtimeUnavailable
    case invalidURL
    case accessDenied
    case invalidCapability
    case duplicateCapability
}

public final class BridraFlutterPlugin: NSObject, FlutterPlugin {
    private static let runtimeStore = RuntimeStore()
    private static let resourceStore = SecurityScopedResourceStore()

    private let streamStore = StreamStore()
    private let downloadStore = DownloadStore()

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
        resourceStore.resetReleased()
    }

    /// Retains native access to a document-picker URL and returns only an
    /// opaque process-local capability for Dart and Go application code.
    /// Call from the main thread after UIDocumentPicker grants the URL.
    public static func grantSecurityScopedResource(_ url: URL) throws -> String {
        guard Thread.isMainThread else {
            throw BridraSecurityScopedResourceError.mainThreadRequired
        }
        guard url.isFileURL, url.path.hasPrefix("/"), !url.path.contains("\0") else {
            throw BridraSecurityScopedResourceError.invalidURL
        }
        guard let runtime = runtimeStore.current() else {
            throw BridraSecurityScopedResourceError.runtimeUnavailable
        }
        guard url.startAccessingSecurityScopedResource() else {
            throw BridraSecurityScopedResourceError.accessDenied
        }
        var retained = false
        defer {
            if !retained {
                url.stopAccessingSecurityScopedResource()
            }
        }
        let capability = try runtime.grantResourcePath(url.path)
        guard validResourceCapability(capability) else {
            try? runtime.releaseResource(capability)
            throw BridraSecurityScopedResourceError.invalidCapability
        }
        do {
            try resourceStore.insert(url, capability: capability)
            retained = true
            return capability
        } catch {
            try? runtime.releaseResource(capability)
            throw error
        }
    }

    /// Releases Go path authority before ending the native security scope.
    /// A duplicate release of a capability issued in this process is a no-op.
    public static func releaseSecurityScopedResource(_ capability: String) throws {
        guard Thread.isMainThread else {
            throw BridraSecurityScopedResourceError.mainThreadRequired
        }
        guard validResourceCapability(capability) else {
            throw BridraSecurityScopedResourceError.invalidCapability
        }
        switch resourceStore.remove(capability) {
        case .active(let url):
            defer { url.stopAccessingSecurityScopedResource() }
            if let runtime = runtimeStore.current() {
                try runtime.releaseResource(capability)
            }
        case .released:
            return
        case .unknown:
            guard let runtime = runtimeStore.current() else {
                throw BridraSecurityScopedResourceError.runtimeUnavailable
            }
            try runtime.releaseResource(capability)
        }
    }

    init(channel: FlutterMethodChannel) {
        self.channel = channel
        super.init()
    }

    public func handle(_ call: FlutterMethodCall, result: @escaping FlutterResult) {
        switch call.method {
        case "call":
            handleCall(call.arguments, result: result)
        case "streamStart":
            handleStreamStart(call.arguments, result: result)
        case "streamNext":
            handleStreamNext(call.arguments, result: result)
        case "streamDispose":
            handleStreamDispose(call.arguments, result: result)
        case "cancel":
            handleCancel(call.arguments, result: result)
        case "downloadOpen":
            handleDownloadOpen(call.arguments, result: result)
        case "downloadRead":
            handleDownloadRead(call.arguments, result: result)
        case "downloadClose":
            handleDownloadClose(call.arguments, result: result)
        case "uploadBegin":
            handleUploadBegin(call.arguments, result: result)
        case "uploadAppend":
            handleUploadAppend(call.arguments, result: result)
        case "uploadStatus":
            handleUploadStatus(call.arguments, result: result)
        case "close":
            handleClose(call.arguments, result: result)
        default:
            result(FlutterMethodNotImplemented)
        }
    }

    private func handleStreamStart(_ arguments: Any?, result: @escaping FlutterResult) {
        guard
            let values = arguments as? [String: Any],
            let requestJSON = values["requestJSON"] as? String
        else {
            result(channelError("invalid_arguments", "streamStart requires requestJSON."))
            return
        }
        guard let runtime = Self.runtimeStore.current() else {
            result(channelError("runtime_unavailable", "No embedded runtime is installed."))
            return
        }
        queue.async {
            do {
                let stream = try runtime.streamJSON(requestJSON)
                let streamID = self.streamStore.insert(stream)
                Self.complete(result, value: streamID)
            } catch {
                Self.complete(result, value: self.runtimeError(error))
            }
        }
    }

    private func handleStreamNext(_ arguments: Any?, result: @escaping FlutterResult) {
        guard
            let values = arguments as? [String: Any],
            let streamID = values["streamID"] as? String,
            !streamID.isEmpty
        else {
            result(channelError("invalid_arguments", "streamNext requires a non-empty streamID."))
            return
        }
        queue.async {
            do {
                let stream = try self.streamStore.beginNext(streamID)
                do {
                    let frame = try stream.nextJSON()
                    self.streamStore.finishNext(streamID, remove: frame == nil)
                    Self.complete(result, value: frame)
                } catch {
                    self.streamStore.finishNext(streamID, remove: true)
                    throw error
                }
            } catch {
                Self.complete(result, value: self.runtimeError(error))
            }
        }
    }

    private func handleStreamDispose(_ arguments: Any?, result: @escaping FlutterResult) {
        guard
            let values = arguments as? [String: Any],
            let streamID = values["streamID"] as? String,
            !streamID.isEmpty
        else {
            result(channelError("invalid_arguments", "streamDispose requires a non-empty streamID."))
            return
        }
        guard let stream = streamStore.remove(streamID) else {
            result(nil)
            return
        }
        guard let runtime = Self.runtimeStore.current() else {
            result(nil)
            return
        }
        queue.async {
            _ = runtime.cancel(stream.requestID())
            Self.complete(result, value: nil)
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

    private func handleDownloadOpen(_ arguments: Any?, result: @escaping FlutterResult) {
        guard
            let values = arguments as? [String: Any],
            let fileID = values["fileID"] as? String,
            !fileID.isEmpty,
            let offset = values["offset"] as? NSNumber,
            offset.int64Value >= 0
        else {
            result(channelError("invalid_arguments", "downloadOpen requires fileID and a non-negative offset."))
            return
        }
        guard let runtime = Self.runtimeStore.current() else {
            result(channelError("runtime_unavailable", "No embedded runtime is installed."))
            return
        }
        queue.async {
            do {
                let download = try runtime.openDownload(fileID, offset: offset.int64Value)
                Self.complete(result, value: self.downloadStore.insert(download))
            } catch {
                Self.complete(result, value: self.runtimeError(error))
            }
        }
    }

    private func handleDownloadRead(_ arguments: Any?, result: @escaping FlutterResult) {
        guard
            let values = arguments as? [String: Any],
            let handle = values["handle"] as? String,
            !handle.isEmpty,
            let maxBytes = values["maxBytes"] as? NSNumber,
            maxBytes.intValue > 0
        else {
            result(channelError("invalid_arguments", "downloadRead requires a handle and positive maxBytes."))
            return
        }
        queue.async {
            do {
                let download = try self.downloadStore.beginRead(handle)
                do {
                    let chunk = try download.nextChunk(maxBytes.intValue)
                    self.downloadStore.finishRead(handle)
                    let value = chunk.map { FlutterStandardTypedData(bytes: $0) }
                    Self.complete(result, value: value)
                } catch {
                    self.downloadStore.finishRead(handle)
                    throw error
                }
            } catch {
                Self.complete(result, value: self.runtimeError(error))
            }
        }
    }

    private func handleDownloadClose(_ arguments: Any?, result: @escaping FlutterResult) {
        guard
            let values = arguments as? [String: Any],
            let handle = values["handle"] as? String,
            !handle.isEmpty,
            let commit = values["commit"] as? Bool
        else {
            result(channelError("invalid_arguments", "downloadClose requires a handle and commit flag."))
            return
        }
        guard let download = downloadStore.remove(handle) else {
            result(nil)
            return
        }
        queue.async {
            do {
                try download.close(commit: commit)
                Self.complete(result, value: nil)
            } catch {
                Self.complete(result, value: self.runtimeError(error))
            }
        }
    }

    private func handleUploadBegin(_ arguments: Any?, result: @escaping FlutterResult) {
        guard
            let values = arguments as? [String: Any],
            let name = values["name"] as? String,
            let mediaType = values["mediaType"] as? String,
            let size = values["size"] as? NSNumber,
            let sha256 = values["sha256"] as? String
        else {
            result(channelError("invalid_arguments", "uploadBegin requires file metadata."))
            return
        }
        guard let runtime = Self.runtimeStore.current() else {
            result(channelError("runtime_unavailable", "No embedded runtime is installed."))
            return
        }
        queue.async {
            do {
                let status = try runtime.beginUploadJSON(
                    name,
                    mediaType: mediaType,
                    size: size.int64Value,
                    sha256: sha256
                )
                Self.complete(result, value: status)
            } catch {
                Self.complete(result, value: self.runtimeError(error))
            }
        }
    }

    private func handleUploadAppend(_ arguments: Any?, result: @escaping FlutterResult) {
        guard
            let values = arguments as? [String: Any],
            let fileID = values["fileID"] as? String,
            !fileID.isEmpty,
            let offset = values["offset"] as? NSNumber,
            offset.int64Value >= 0,
            let chunk = values["chunk"] as? FlutterStandardTypedData,
            !chunk.data.isEmpty
        else {
            result(channelError("invalid_arguments", "uploadAppend requires fileID, offset, and bytes."))
            return
        }
        guard let runtime = Self.runtimeStore.current() else {
            result(channelError("runtime_unavailable", "No embedded runtime is installed."))
            return
        }
        queue.async {
            do {
                let status = try runtime.appendUploadJSON(
                    fileID,
                    offset: offset.int64Value,
                    chunk: chunk.data
                )
                Self.complete(result, value: status)
            } catch {
                Self.complete(result, value: self.runtimeError(error))
            }
        }
    }

    private func handleUploadStatus(_ arguments: Any?, result: @escaping FlutterResult) {
        guard
            let values = arguments as? [String: Any],
            let fileID = values["fileID"] as? String,
            !fileID.isEmpty
        else {
            result(channelError("invalid_arguments", "uploadStatus requires fileID."))
            return
        }
        guard let runtime = Self.runtimeStore.current() else {
            result(channelError("runtime_unavailable", "No embedded runtime is installed."))
            return
        }
        queue.async {
            do {
                Self.complete(result, value: try runtime.uploadStatusJSON(fileID))
            } catch {
                Self.complete(result, value: self.runtimeError(error))
            }
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
            var closeError: Error?
            for download in self.downloadStore.removeAll() {
                do {
                    try download.close(commit: false)
                } catch {
                    closeError = closeError ?? error
                }
            }
            for resource in Self.resourceStore.removeAll() {
                do {
                    try runtime.releaseResource(resource.capability)
                } catch {
                    closeError = closeError ?? error
                }
                resource.url.stopAccessingSecurityScopedResource()
            }
            do {
                try runtime.close(timeoutMilliseconds: milliseconds.int64Value)
                self.streamStore.removeAll()
                Self.runtimeStore.finishClose(succeeded: true)
                if let closeError {
                    Self.complete(result, value: self.runtimeError(closeError))
                } else {
                    Self.complete(result, value: nil)
                }
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

private func validResourceCapability(_ value: String) -> Bool {
    value.utf8.count == 96 && value.utf8.allSatisfy {
        ($0 >= 48 && $0 <= 57) || ($0 >= 97 && $0 <= 102)
    }
}

private enum SecurityScopedResourceRemoval {
    case active(URL)
    case released
    case unknown
}

private struct SecurityScopedResourceEntry {
    let capability: String
    let url: URL
}

private final class SecurityScopedResourceStore {
    private let lock = NSLock()
    private var resources: [String: URL] = [:]
    private var released: Set<String> = []

    func insert(_ url: URL, capability: String) throws {
        lock.lock()
        defer { lock.unlock() }
        guard resources[capability] == nil else {
            throw BridraSecurityScopedResourceError.duplicateCapability
        }
        resources[capability] = url
        released.remove(capability)
    }

    func remove(_ capability: String) -> SecurityScopedResourceRemoval {
        lock.lock()
        defer { lock.unlock() }
        if let url = resources.removeValue(forKey: capability) {
            released.insert(capability)
            return .active(url)
        }
        return released.contains(capability) ? .released : .unknown
    }

    func removeAll() -> [SecurityScopedResourceEntry] {
        lock.lock()
        defer { lock.unlock() }
        let entries = resources.map {
            SecurityScopedResourceEntry(capability: $0.key, url: $0.value)
        }
        released.formUnion(resources.keys)
        resources.removeAll()
        return entries
    }

    func resetReleased() {
        lock.lock()
        defer { lock.unlock() }
        if resources.isEmpty {
            released.removeAll()
        }
    }
}

private enum DownloadStoreError: Error {
    case unavailable
    case readAlreadyPending
}

private final class DownloadStore {
    private let lock = NSLock()
    private var downloads: [String: any BridraEmbeddedDownload] = [:]
    private var pendingReads: Set<String> = []

    func insert(_ download: any BridraEmbeddedDownload) -> String {
        lock.lock()
        defer { lock.unlock() }
        let id = UUID().uuidString
        downloads[id] = download
        return id
    }

    func beginRead(_ id: String) throws -> any BridraEmbeddedDownload {
        lock.lock()
        defer { lock.unlock() }
        guard let download = downloads[id] else {
            throw DownloadStoreError.unavailable
        }
        guard !pendingReads.contains(id) else {
            throw DownloadStoreError.readAlreadyPending
        }
        pendingReads.insert(id)
        return download
    }

    func finishRead(_ id: String) {
        lock.lock()
        defer { lock.unlock() }
        pendingReads.remove(id)
    }

    func remove(_ id: String) -> (any BridraEmbeddedDownload)? {
        lock.lock()
        defer { lock.unlock() }
        pendingReads.remove(id)
        return downloads.removeValue(forKey: id)
    }

    func removeAll() -> [any BridraEmbeddedDownload] {
        lock.lock()
        defer { lock.unlock() }
        let values = Array(downloads.values)
        downloads.removeAll()
        pendingReads.removeAll()
        return values
    }
}

private enum StreamStoreError: Error {
    case unavailable
    case nextAlreadyPending
}

private final class StreamStore {
    private let lock = NSLock()
    private var streams: [String: any BridraEmbeddedStream] = [:]
    private var pendingNext: Set<String> = []

    func insert(_ stream: any BridraEmbeddedStream) -> String {
        lock.lock()
        defer { lock.unlock() }
        let id = UUID().uuidString
        streams[id] = stream
        return id
    }

    func beginNext(_ id: String) throws -> any BridraEmbeddedStream {
        lock.lock()
        defer { lock.unlock() }
        guard let stream = streams[id] else {
            throw StreamStoreError.unavailable
        }
        guard !pendingNext.contains(id) else {
            throw StreamStoreError.nextAlreadyPending
        }
        pendingNext.insert(id)
        return stream
    }

    func finishNext(_ id: String, remove: Bool) {
        lock.lock()
        defer { lock.unlock() }
        pendingNext.remove(id)
        if remove {
            streams.removeValue(forKey: id)
        }
    }

    func remove(_ id: String) -> (any BridraEmbeddedStream)? {
        lock.lock()
        defer { lock.unlock() }
        pendingNext.remove(id)
        return streams.removeValue(forKey: id)
    }

    func removeAll() {
        lock.lock()
        defer { lock.unlock() }
        streams.removeAll()
        pendingNext.removeAll()
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
