#define UNICODE
#define _UNICODE
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <winhttp.h>
#include <wrl.h>
#include <WebView2.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.Data.Json.h>
#include <filesystem>
#include <memory>
#include <stdexcept>
#include <string>

using Microsoft::WRL::Callback;
using Microsoft::WRL::ComPtr;
using winrt::Windows::Data::Json::JsonObject;

namespace {
HWND window;
int exitCode;
std::wstring origin, bootstrap, title;
INTERNET_PORT port;
ComPtr<ICoreWebView2Environment> environment;
ComPtr<ICoreWebView2Controller> controller;
ComPtr<ICoreWebView2> webView;

void require(bool condition, const char* message) {
    if (!condition) throw std::runtime_error(message);
}
void check(HRESULT hr, const char* message) { require(SUCCEEDED(hr), message); }
void fail(const char* message) {
    if (exitCode) return;
    exitCode = 1;
    auto line = std::string("Bridra WebView2: ") + message + "\n";
    DWORD written;
    WriteFile(GetStdHandle(STD_ERROR_HANDLE), line.data(), DWORD(line.size()), &written, nullptr);
    if (window) PostMessageW(window, WM_CLOSE, 0, 0);
}
template<class Function> HRESULT guarded(Function fn) noexcept {
    try { fn(); } catch (const std::runtime_error& err) { fail(err.what()); }
    catch (...) { fail("could not initialize the desktop window"); }
    return S_OK;
}
bool validSecret(const std::wstring& value) {
    return value.size() == 64 && value.find_first_not_of(L"0123456789abcdef") == std::wstring::npos;
}
void readConfiguration() {
    std::string line;
    for (;;) {
        char ch{}; DWORD count{};
        require(line.size() < 8192 && ReadFile(GetStdHandle(STD_INPUT_HANDLE), &ch, 1, &count, nullptr) && count,
                "missing launcher configuration");
        if (ch == '\n') break;
        line += ch;
    }
    auto config = JsonObject::Parse(winrt::to_hstring(line));
    require(config.GetNamedNumber(L"version") == 1, "unsupported launcher protocol");
    origin = config.GetNamedString(L"origin");
    bootstrap = config.GetNamedString(L"bootstrap");
    title = config.GetNamedString(L"title");
    std::wstring prefix = L"http://127.0.0.1:";
    require(origin.starts_with(prefix) && validSecret(bootstrap) && !title.empty(), "invalid launcher configuration");
    auto number = origin.substr(prefix.size());
    require(!number.empty() && number.size() <= 5 && number.front() != L'0' &&
            number.find_first_not_of(L"0123456789") == std::wstring::npos && std::stoi(number) <= 65535, "invalid loopback port");
    port = static_cast<INTERNET_PORT>(std::stoi(number));
}
DWORD WINAPI watchLauncher(LPVOID target) {
    char ignored; DWORD count;
    ReadFile(GetStdHandle(STD_INPUT_HANDLE), &ignored, 1, &count, nullptr);
    PostMessageW(static_cast<HWND>(target), WM_CLOSE, 0, 0);
    return 0;
}
struct InternetCloser { void operator()(HINTERNET handle) const { WinHttpCloseHandle(handle); } };
using Internet = std::unique_ptr<void, InternetCloser>;
void installSession() {
    Internet client(WinHttpOpen(L"Bridra", WINHTTP_ACCESS_TYPE_NO_PROXY, nullptr, nullptr, 0));
    require(client && WinHttpSetTimeouts(client.get(), 3000, 3000, 3000, 4000), "could not initialize loopback HTTP");
    Internet connection(WinHttpConnect(client.get(), L"127.0.0.1", port, 0));
    require(bool(connection), "could not connect to the local backend");
    Internet request(WinHttpOpenRequest(connection.get(), L"POST", L"/__bootstrap", nullptr,
                                        WINHTTP_NO_REFERER, WINHTTP_DEFAULT_ACCEPT_TYPES, 0));
    DWORD disabled = WINHTTP_DISABLE_REDIRECTS | WINHTTP_DISABLE_COOKIES | WINHTTP_DISABLE_AUTHENTICATION;
    require(request && WinHttpSetOption(request.get(), WINHTTP_OPTION_DISABLE_FEATURE, &disabled, sizeof(disabled)),
            "could not configure the handshake");
    auto header = L"Authorization: Bearer " + bootstrap + L"\r\n";
    bootstrap.clear();
    require(WinHttpSendRequest(request.get(), header.c_str(), DWORD(header.size()), nullptr, 0, 0, 0) &&
            WinHttpReceiveResponse(request.get(), nullptr), "handshake failed");
    DWORD status, count = sizeof(status);
    require(WinHttpQueryHeaders(request.get(), WINHTTP_QUERY_STATUS_CODE | WINHTTP_QUERY_FLAG_NUMBER, nullptr,
                                &status, &count, nullptr) && status == 200, "handshake rejected");
    char buffer[512]; std::string body;
    do {
        require(WinHttpReadData(request.get(), buffer, sizeof(buffer), &count), "invalid handshake response");
        body.append(buffer, count);
        require(body.size() <= 2048, "oversized handshake response");
    } while (count);
    auto response = JsonObject::Parse(winrt::to_hstring(body));
    auto session = response.GetNamedString(L"session");
    require(response.GetNamedNumber(L"version") == 1 && validSecret(std::wstring(session)), "invalid session");
    ComPtr<ICoreWebView2_2> cookieView;
    ComPtr<ICoreWebView2CookieManager> manager;
    ComPtr<ICoreWebView2Cookie> cookie;
    check(webView.As(&cookieView), "WebView2 runtime needs cookie support");
    check(cookieView->get_CookieManager(&manager), "could not access the private session");
    check(manager->CreateCookie(L"bridra_wk", session.c_str(), L"127.0.0.1", L"/rpc", &cookie), "could not create session cookie");
    check(cookie->put_IsHttpOnly(TRUE), "could not protect the session cookie");
    check(cookie->put_SameSite(COREWEBVIEW2_COOKIE_SAME_SITE_KIND_STRICT), "could not restrict the session cookie");
    check(manager->AddOrUpdateCookie(cookie.Get()), "could not install the session cookie");
}
bool allowedUri(LPWSTR uri) {
    bool allowed = uri && std::wstring_view(uri).starts_with(origin + L"/");
    CoTaskMemFree(uri);
    return allowed;
}
void configureView(ICoreWebView2Controller* value) {
    controller = value;
    check(controller->get_CoreWebView2(&webView), "could not access WebView2");
    ComPtr<ICoreWebView2Settings> settings;
    check(webView->get_Settings(&settings), "could not configure WebView2");
    check(settings->put_IsWebMessageEnabled(FALSE), "could not disable web messaging");
    check(settings->put_AreHostObjectsAllowed(FALSE), "could not disable host objects");
    check(settings->put_AreDevToolsEnabled(FALSE), "could not disable developer tools");
    check(settings->put_AreDefaultScriptDialogsEnabled(FALSE), "could not disable script dialogs");
    check(settings->put_AreDefaultContextMenusEnabled(FALSE), "could not disable the context menu");
    RECT bounds; GetClientRect(window, &bounds);
    check(controller->put_Bounds(bounds), "could not size the window");
    EventRegistrationToken token;
    check(webView->add_NavigationStarting(Callback<ICoreWebView2NavigationStartingEventHandler>(
        [](ICoreWebView2*, ICoreWebView2NavigationStartingEventArgs* args) -> HRESULT {
            LPWSTR uri = nullptr;
            args->get_Uri(&uri);
            return allowedUri(uri) ? S_OK : args->put_Cancel(TRUE);
        }).Get(), &token), "could not restrict navigation");
    check(webView->add_FrameNavigationStarting(Callback<ICoreWebView2NavigationStartingEventHandler>(
        [](ICoreWebView2*, ICoreWebView2NavigationStartingEventArgs* args) { return args->put_Cancel(TRUE); }
    ).Get(), &token), "could not restrict frames");
    check(webView->add_NewWindowRequested(Callback<ICoreWebView2NewWindowRequestedEventHandler>(
        [](ICoreWebView2*, ICoreWebView2NewWindowRequestedEventArgs* args) { return args->put_Handled(TRUE); }
    ).Get(), &token), "could not restrict popups");
    check(webView->add_PermissionRequested(Callback<ICoreWebView2PermissionRequestedEventHandler>(
        [](ICoreWebView2*, ICoreWebView2PermissionRequestedEventArgs* args) { return args->put_State(COREWEBVIEW2_PERMISSION_STATE_DENY); }
    ).Get(), &token), "could not restrict permissions");
    check(webView->AddWebResourceRequestedFilter(L"*", COREWEBVIEW2_WEB_RESOURCE_CONTEXT_ALL), "could not filter resources");
    check(webView->add_WebResourceRequested(Callback<ICoreWebView2WebResourceRequestedEventHandler>(
        [](ICoreWebView2*, ICoreWebView2WebResourceRequestedEventArgs* args) -> HRESULT {
            return guarded([&] {
                ComPtr<ICoreWebView2WebResourceRequest> request;
                LPWSTR uri = nullptr;
                check(args->get_Request(&request), "invalid resource request");
                check(request->get_Uri(&uri), "invalid resource URL");
                if (!allowedUri(uri)) {
                    ComPtr<ICoreWebView2WebResourceResponse> blocked;
                    check(environment->CreateWebResourceResponse(nullptr, 403, L"Forbidden", L"", &blocked), "could not reject external resource");
                    check(args->put_Response(blocked.Get()), "could not block external resource");
                }
            });
        }).Get(), &token), "could not restrict resources");
    check(webView->add_ProcessFailed(Callback<ICoreWebView2ProcessFailedEventHandler>(
        [](ICoreWebView2*, ICoreWebView2ProcessFailedEventArgs*) -> HRESULT { fail("WebView2 process failed"); return S_OK; }
    ).Get(), &token), "could not monitor WebView2");
    installSession();
    check(webView->Navigate((origin + L"/").c_str()), "could not open the desktop page");
    KillTimer(window, 1);
}
void startWebView() {
    auto profile = std::filesystem::current_path() / L"webview-profile";
    require(!std::filesystem::exists(profile), "WebView2 needs a new private profile");
    check(CreateCoreWebView2EnvironmentWithOptions(nullptr, profile.c_str(), nullptr,
        Callback<ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler>(
            [](HRESULT hr, ICoreWebView2Environment* value) -> HRESULT {
                return guarded([&] {
                    check(hr, "could not start the WebView2 Runtime");
                    environment = value;
                    ComPtr<ICoreWebView2Environment10> privateEnvironment;
                    ComPtr<ICoreWebView2ControllerOptions> options;
                    check(environment.As(&privateEnvironment), "WebView2 runtime needs private profile support");
                    check(privateEnvironment->CreateCoreWebView2ControllerOptions(&options), "could not configure the private profile");
                    check(options->put_IsInPrivateModeEnabled(TRUE), "could not enable private browsing");
                    check(privateEnvironment->CreateCoreWebView2ControllerWithOptions(window, options.Get(),
                        Callback<ICoreWebView2CreateCoreWebView2ControllerCompletedHandler>(
                            [](HRESULT result, ICoreWebView2Controller* c) -> HRESULT {
                                return guarded([&] { check(result, "could not initialize the window"); configureView(c); });
                            }).Get()), "could not start the private window");
                });
            }).Get()), "install or update Microsoft Edge WebView2 Runtime");
}
LRESULT CALLBACK handleMessage(HWND hwnd, UINT message, WPARAM wp, LPARAM lp) {
    switch (message) {
    case WM_SIZE:
        if (controller) { RECT size; GetClientRect(hwnd, &size); controller->put_Bounds(size); }
        return 0;
    case WM_SETFOCUS:
        if (controller) controller->MoveFocus(COREWEBVIEW2_MOVE_FOCUS_REASON_PROGRAMMATIC);
        return 0;
    case WM_TIMER:
        fail("window initialization timed out"); return 0;
    case WM_CLOSE:
        KillTimer(hwnd, 1);
        if (controller) controller->Close();
        webView.Reset(); controller.Reset(); environment.Reset();
        DestroyWindow(hwnd); return 0;
    case WM_DESTROY:
        PostQuitMessage(exitCode); return 0;
    }
    return DefWindowProcW(hwnd, message, wp, lp);
}
} // namespace

int WINAPI wWinMain(HINSTANCE instance, HINSTANCE, PWSTR, int) {
    HANDLE watcher = nullptr;
    guarded([&] {
        winrt::init_apartment(winrt::apartment_type::single_threaded);
        readConfiguration();
        SetProcessDpiAwarenessContext(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2);
        WNDCLASSW wc{};
        wc.lpfnWndProc = handleMessage; wc.hInstance = instance;
        wc.lpszClassName = L"BridraWebView"; wc.hCursor = LoadCursorW(nullptr, IDC_ARROW);
        require(RegisterClassW(&wc) != 0, "could not register the window");
        window = CreateWindowExW(0, wc.lpszClassName, title.c_str(), WS_OVERLAPPEDWINDOW,
            CW_USEDEFAULT, CW_USEDEFAULT, 900, 640, nullptr, nullptr, instance, nullptr);
        require(window != nullptr, "could not create the window");
        ShowWindow(window, SW_SHOW);
        watcher = CreateThread(nullptr, 0, watchLauncher, window, 0, nullptr);
        require(watcher != nullptr && SetTimer(window, 1, 35000, nullptr), "could not monitor initialization");
        startWebView();
        MSG message; int result;
        while ((result = GetMessageW(&message, nullptr, 0, 0)) > 0) {
            TranslateMessage(&message); DispatchMessageW(&message);
        }
        require(result >= 0, "window message loop failed");
    });
    if (watcher) { CancelSynchronousIo(watcher); CloseHandle(watcher); }
    return exitCode;
}
