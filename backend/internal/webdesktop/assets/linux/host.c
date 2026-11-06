#define _POSIX_C_SOURCE 200809L
#include <errno.h>
#include <glib-unix.h>
#include <gtk/gtk.h>
#include <libsoup/soup.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <webkit/webkit.h>

static GtkWindow *window;
static WebKitWebView *view;
static WebKitNetworkSession *network;
static char *page_url;

static void fail(const char *reason) {
    g_printerr("Bridra WebView: %s\n", reason);
    exit(EXIT_FAILURE);
}

static gboolean is_secret(const char *text) {
    return text && strlen(text) == 64 && strspn(text, "0123456789abcdef") == 64;
}

static char *string_field(JSCValue *object, const char *key) {
    JSCValue *value = jsc_value_object_get_property(object, key);
    char *text = jsc_value_is_string(value) ? jsc_value_to_string(value) : NULL;
    g_object_unref(value);
    return text;
}

static JSCValue *decode(JSCContext *context, const char *bytes) {
    JSCValue *object = jsc_value_new_from_json(context, bytes);
    if (!object || !jsc_value_is_object(object))
        fail("Invalid native handshake document.");
    JSCValue *version = jsc_value_object_get_property(object, "version");
    if (!jsc_value_is_number(version) || jsc_value_to_double(version) != 1)
        fail("Unsupported native handshake version.");
    g_object_unref(version);
    return object;
}

static char *exchange(JSCContext *context, const char *origin, const char *proof) {
    SoupSession *client = soup_session_new();
    soup_session_set_timeout(client, 5);
    GProxyResolver *direct = g_simple_proxy_resolver_new(NULL, NULL);
    soup_session_set_proxy_resolver(client, direct);
    g_object_unref(direct);

    char *endpoint = g_strconcat(origin, "/__bootstrap", NULL);
    SoupMessage *request = soup_message_new("POST", endpoint);
    g_free(endpoint);
    soup_message_set_flags(request, SOUP_MESSAGE_NO_REDIRECT);
    char *auth = g_strconcat("Bearer ", proof, NULL);
    soup_message_headers_replace(soup_message_get_request_headers(request), "Authorization", auth);
    g_free(auth);
    GBytes *body = soup_session_send_and_read(client, request, NULL, NULL);
    if (!body || soup_message_get_status(request) != 200 || g_bytes_get_size(body) > 2048)
        fail("Local session handshake failed.");
    const char *type = soup_message_headers_get_content_type(soup_message_get_response_headers(request), NULL);
    if (g_strcmp0(type, "application/json") != 0)
        fail("Invalid local session response.");

    char *payload = g_strndup(g_bytes_get_data(body, NULL), g_bytes_get_size(body));
    JSCValue *reply = decode(context, payload);
    char *session = string_field(reply, "session");
    if (!is_secret(session)) fail("Invalid local session.");
    g_free(payload);
    g_object_unref(reply);
    g_bytes_unref(body);
    g_object_unref(request);
    g_object_unref(client);
    return session;
}

static void installed(GObject *object, GAsyncResult *result, gpointer unused) {
    (void)unused;
    if (!webkit_cookie_manager_add_cookie_finish(WEBKIT_COOKIE_MANAGER(object), result, NULL))
        fail("Cannot install private session.");
    webkit_web_view_load_uri(view, page_url);
}

static void bootstrap(void) {
    char line[8192];
    if (!fgets(line, sizeof line, stdin) || !strchr(line, '\n'))
        fail("Missing private startup message.");
    JSCContext *context = jsc_context_new();
    JSCValue *config = decode(context, line);
    char *origin = string_field(config, "origin");
    char *proof = string_field(config, "bootstrap");
    char *title = string_field(config, "title");
    g_object_unref(config);
    if (!origin || !is_secret(proof) || !title || !*title)
        fail("Invalid private startup fields.");
    GUri *site = g_uri_parse(origin, G_URI_FLAGS_NONE, NULL);
    int port = site ? g_uri_get_port(site) : -1;
    char *canonical = g_strdup_printf("http://127.0.0.1:%d", port);
    if (port < 1 || port > 65535 || strcmp(canonical, origin) != 0)
        fail("Invalid local application origin.");
    g_free(canonical);
    gtk_window_set_title(window, title);
    g_free(title);

    page_url = g_strconcat(origin, "/", NULL);
    char *session = exchange(context, origin, proof);
    g_free(proof);
    g_free(origin);
    g_object_unref(context);
    // Omitting Domain binds the cookie to the loopback host; /rpc is its only application path.
    char *header = g_strconcat("bridra_wk=", session, "; HttpOnly; SameSite=Strict; Path=/rpc", NULL);
    SoupCookie *cookie = soup_cookie_parse(header, site);
    g_free(session);
    g_free(header);
    g_uri_unref(site);
    if (!cookie || !soup_cookie_get_http_only(cookie) || soup_cookie_get_expires(cookie) ||
        soup_cookie_get_same_site_policy(cookie) != SOUP_SAME_SITE_POLICY_STRICT)
        fail("Cannot construct private session.");
    webkit_cookie_manager_add_cookie(webkit_network_session_get_cookie_manager(network),
                                     cookie, NULL, installed, NULL);
    soup_cookie_free(cookie);
}

static gboolean same_origin(const char *uri) {
    // page_url includes the slash after the validated numeric authority.
    return uri && page_url && g_str_has_prefix(uri, page_url);
}

static gboolean decide(WebKitWebView *webview, WebKitPolicyDecision *decision,
                       WebKitPolicyDecisionType type, gpointer unused) {
    (void)webview;
    (void)unused;
    switch (type) {
    case WEBKIT_POLICY_DECISION_TYPE_NEW_WINDOW_ACTION:
        break;
    case WEBKIT_POLICY_DECISION_TYPE_NAVIGATION_ACTION: {
        WebKitNavigationAction *action = webkit_navigation_policy_decision_get_navigation_action(
            WEBKIT_NAVIGATION_POLICY_DECISION(decision));
        WebKitURIRequest *request = webkit_navigation_action_get_request(action);
        if (same_origin(webkit_uri_request_get_uri(request)) &&
            g_strcmp0(webkit_uri_request_get_http_method(request), "GET") == 0)
            return FALSE;
        break;
    }
    case WEBKIT_POLICY_DECISION_TYPE_RESPONSE: {
        WebKitResponsePolicyDecision *policy = WEBKIT_RESPONSE_POLICY_DECISION(decision);
        WebKitURIResponse *response = webkit_response_policy_decision_get_response(policy);
        if (same_origin(webkit_uri_response_get_uri(response)) &&
            webkit_response_policy_decision_is_mime_type_supported(policy))
            return FALSE;
        break;
    }
    default:
        break;
    }
    webkit_policy_decision_ignore(decision);
    return TRUE;
}

static gboolean deny_permission(WebKitWebView *webview, WebKitPermissionRequest *request, gpointer unused) {
    (void)webview;
    (void)unused;
    webkit_permission_request_deny(request);
    return TRUE;
}

static void renderer_stopped(WebKitWebView *webview, WebKitWebProcessTerminationReason reason, gpointer unused) {
    (void)webview;
    (void)reason;
    (void)unused;
    fail("Web content process stopped.");
}

static gboolean supervisor_closed(gint fd, GIOCondition event, gpointer unused) {
    (void)fd;
    (void)event;
    (void)unused;
    gtk_window_close(window);
    return G_SOURCE_REMOVE;
}

static void activate(GtkApplication *app, gpointer unused) {
    (void)unused;
    if (window) {
        gtk_window_present(window);
        return;
    }
    window = GTK_WINDOW(gtk_application_window_new(app));
    gtk_window_set_default_size(window, 880, 610);
    gtk_window_set_titlebar(window, gtk_header_bar_new());
    network = webkit_network_session_new_ephemeral();
    webkit_network_session_set_proxy_settings(network, WEBKIT_NETWORK_PROXY_MODE_NO_PROXY, NULL);
    view = WEBKIT_WEB_VIEW(g_object_new(WEBKIT_TYPE_WEB_VIEW, "network-session", network, NULL));
    gtk_window_set_child(window, GTK_WIDGET(view));
    g_signal_connect(view, "decide-policy", G_CALLBACK(decide), NULL);
    g_signal_connect(view, "permission-request", G_CALLBACK(deny_permission), NULL);
    g_signal_connect(view, "web-process-terminated", G_CALLBACK(renderer_stopped), NULL);
    bootstrap();
    g_unix_fd_add(STDIN_FILENO, G_IO_IN | G_IO_HUP | G_IO_ERR, supervisor_closed, NULL);
    gtk_window_present(window);
}

int main(int argc, char **argv) {
    GtkApplication *app = gtk_application_new("org.bridra.WebView", G_APPLICATION_NON_UNIQUE);
    g_signal_connect(app, "activate", G_CALLBACK(activate), NULL);
    int code = g_application_run(G_APPLICATION(app), argc, argv);
    g_object_unref(app);
    g_clear_object(&network);
    g_free(page_url);
    return code;
}
