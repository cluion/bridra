import AppKit
import Foundation
import WebKit

struct Launch: Decodable {
    let version: Int
    let origin: String
    var bootstrap: String
    let title: String

    var baseURL: URL? {
        guard version == 1, !title.isEmpty, validSecret(bootstrap),
              let url = URLComponents(string: origin), let port = url.port,
              (1...65535).contains(port), origin == "http://127.0.0.1:\(port)" else { return nil }
        return url.url
    }
}

private func validSecret(_ value: String) -> Bool {
    value.utf8.count == 64 && value.utf8.allSatisfy { (48...57).contains($0) || (97...102).contains($0) }
}

private func fatalHost(_ message: String) -> Never {
    FileHandle.standardError.write(Data(("Bridra WebView: " + message + "\n").utf8))
    exit(EXIT_FAILURE)
}

private func readLaunch() -> Launch? {
    var data = Data()
    var byte: UInt8 = 0
    while data.count < 8192 {
        let count = Darwin.read(STDIN_FILENO, &byte, 1)
        if count < 0 && errno == EINTR { continue }
        guard count == 1 else { return nil }
        if byte == 10 { return try? JSONDecoder().decode(Launch.self, from: data) }
        data.append(byte)
    }
    return nil
}

private final class NoRedirects: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask,
                    willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest,
                    completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}

@MainActor
private final class Host: NSObject, NSApplicationDelegate, NSWindowDelegate, WKNavigationDelegate, WKUIDelegate {
    private let base: URL
    private let title: String
    private var proof: String?
    private var installed = false
    private var ready = false
    private var navigationID = 0
    private var closing = false
    private var window: NSWindow!
    private var view: WKWebView!

    init(launch: Launch, baseURL: URL) {
        title = launch.title
        base = baseURL
        proof = launch.bootstrap
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .nonPersistent()
        configuration.preferences.javaScriptCanOpenWindowsAutomatically = false
        view = WKWebView(frame: NSRect(x: 0, y: 0, width: 900, height: 620), configuration: configuration)
        view.navigationDelegate = self
        view.uiDelegate = self
        window = NSWindow(contentRect: view.frame, styleMask: [.titled, .resizable, .closable, .miniaturizable],
                          backing: .buffered, defer: false)
        window.title = title
        window.delegate = self
        window.isReleasedWhenClosed = false
        window.contentView = view
        window.center()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)

        // The launcher holds the pipe for its entire lifetime.
        DispatchQueue.global(qos: .utility).async {
            var byte: UInt8 = 0
            repeat {
                let n = Darwin.read(STDIN_FILENO, &byte, 1)
                if n < 0 && errno == EINTR { continue }
                break
            } while true
            DispatchQueue.main.async {
                if !self.closing { fatalHost("lost launcher connection") }
            }
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 30) {
            if !self.ready && !self.closing { fatalHost("timed out loading the local application") }
        }
        view.load(URLRequest(url: base.appendingPathComponent("")))
    }

    private func isRoot(_ url: URL?) -> Bool {
        guard let url else { return false }
        return url.scheme == "http" && url.host == "127.0.0.1" && url.port == base.port &&
               url.user == nil && url.password == nil && url.query == nil && ["", "/"].contains(url.path)
    }

    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        guard action.targetFrame?.isMainFrame == true, action.sourceFrame.isMainFrame,
              isRoot(action.request.url), action.request.httpMethod == "GET", !action.shouldPerformDownload else {
            decisionHandler(.cancel)
            return
        }
        navigationID += 1
        decisionHandler(.allow)
    }

    private func trustedResponse(_ response: WKNavigationResponse) -> Bool {
        guard response.isForMainFrame, let http = response.response as? HTTPURLResponse,
              isRoot(http.url), http.statusCode == 200, http.mimeType == "text/html",
              let csp = http.value(forHTTPHeaderField: "Content-Security-Policy") else { return false }
        let rules = csp.split(separator: ";").map { $0.split(whereSeparator: { $0.isWhitespace }).joined(separator: " ") }
        let keys = rules.compactMap { $0.split(separator: " ").first }
        let required = ["default-src 'self'", "script-src 'self'", "style-src 'self'", "connect-src 'self'", "img-src 'self'",
                        "object-src 'none'", "base-uri 'none'", "frame-src 'none'",
                        "frame-ancestors 'none'", "form-action 'none'"]
        return Set(keys).count == rules.count && Set(required) == Set(rules)
    }

    func webView(_ webView: WKWebView, decidePolicyFor response: WKNavigationResponse,
                 decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void) {
        guard trustedResponse(response) else {
            decisionHandler(.cancel)
            if response.isForMainFrame { fatalHost("local application document rejected") }
            return
        }
        if installed { decisionHandler(.allow); return }
        guard let token = proof else { decisionHandler(.cancel); return }
        proof = nil
        let id = navigationID
        // Hold the trusted document until its very first script can make an authenticated RPC.
        Task {
            do {
                try await installSession(token)
                guard !closing, id == navigationID else { decisionHandler(.cancel); return }
                installed = true
                decisionHandler(.allow)
            } catch {
                decisionHandler(.cancel)
                if !closing { fatalHost("local application handshake failed") }
            }
        }
    }

    private func installSession(_ token: String) async throws {
        var request = URLRequest(url: base.appendingPathComponent("__bootstrap"))
        request.httpMethod = "POST"
        request.httpShouldHandleCookies = false
        request.setValue("Bearer " + token, forHTTPHeaderField: "Authorization")
        let config = URLSessionConfiguration.ephemeral
        config.httpCookieStorage = nil
        config.urlCredentialStorage = nil
        config.connectionProxyDictionary = [:]
        config.urlCache = nil
        config.timeoutIntervalForResource = 6
        let session = URLSession(configuration: config, delegate: NoRedirects(), delegateQueue: nil)
        defer { session.invalidateAndCancel() }
        let (data, raw) = try await session.data(for: request)
        struct Exchange: Decodable { let version: Int; let session: String }
        guard let http = raw as? HTTPURLResponse, http.statusCode == 200, http.url == request.url,
              http.mimeType == "application/json", data.count <= 2048,
              let reply = try? JSONDecoder().decode(Exchange.self, from: data),
              reply.version == 1, validSecret(reply.session) else { throw URLError(.badServerResponse) }
        let attributes: [HTTPCookiePropertyKey: Any] = [
            .originURL: base, .name: "bridra_wk", .value: reply.session, .path: "/rpc", .discard: "TRUE",
            HTTPCookiePropertyKey("HttpOnly"): "TRUE", HTTPCookiePropertyKey("SameSite"): "Strict"
        ]
        guard let cookie = HTTPCookie(properties: attributes), cookie.isHTTPOnly, cookie.isSessionOnly else {
            throw URLError(.cannotParseResponse)
        }
        await view.configuration.websiteDataStore.httpCookieStore.setCookie(cookie)
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        if !ready && installed && isRoot(webView.url) {
            ready = true
            FileHandle.standardOutput.write(Data("{\"event\":\"ready\"}\n".utf8))
        }
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) { fatalHost("web content process terminated") }

    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                 for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? { nil }
    func webView(_ webView: WKWebView, requestMediaCapturePermissionFor origin: WKSecurityOrigin,
                 initiatedByFrame frame: WKFrameInfo, type: WKMediaCaptureType,
                 decisionHandler: @escaping (WKPermissionDecision) -> Void) { decisionHandler(.deny) }
    @available(macOS 27.0, *)
    func webView(_ webView: WKWebView, requestGeolocationPermissionFor origin: WKSecurityOrigin,
                 initiatedByFrame frame: WKFrameInfo,
                 decisionHandler: @escaping (WKPermissionDecision) -> Void) { decisionHandler(.deny) }
    func webView(_ webView: WKWebView, runOpenPanelWith parameters: WKOpenPanelParameters,
                 initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping ([URL]?) -> Void) {
        completionHandler(nil)
    }

    func windowWillClose(_ notification: Notification) {
        closing = true
        view.stopLoading()
        NSApp.terminate(nil)
    }
}

guard var launch = readLaunch(), let url = launch.baseURL else { fatalHost("invalid private launch input") }
MainActor.assumeIsolated {
    let host = Host(launch: launch, baseURL: url)
    launch.bootstrap = ""
    let application = NSApplication.shared
    application.setActivationPolicy(.regular)
    application.delegate = host
    application.run()
    withExtendedLifetime(host) {}
}
