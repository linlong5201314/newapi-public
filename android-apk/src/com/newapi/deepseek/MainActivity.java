package com.newapi.deepseek;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.ActivityNotFoundException;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.database.Cursor;
import android.graphics.Color;
import android.net.Uri;
import android.net.http.SslError;
import android.os.Build;
import android.os.Bundle;
import android.provider.OpenableColumns;
import android.view.View;
import android.view.ViewGroup;
import android.webkit.CookieManager;
import android.webkit.MimeTypeMap;
import android.webkit.PermissionRequest;
import android.webkit.SslErrorHandler;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.TextView;
import android.widget.Toast;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

public class MainActivity extends Activity {
    private static final int REQUEST_CODE_FILE_CHOOSER = 41;
    private static final int REQUEST_CODE_RECORD_AUDIO = 42;
    private static final String PREFS_NAME = "update_check";
    private static final String PREF_LAST_NEGATIVE_PROBE = "last_negative_probe_millis";
    private static final String UPDATE_DOWNLOAD_PATH = "/deepseek-newapi.apk";

    private WebView webView;
    private LinearLayout errorPanel;
    private ProgressBar progress;
    private LinearLayout updateBanner;
    private TextView updateBannerText;
    private final AppPolicy policy = new AppPolicy(BuildConfig.SITE_ORIGIN);
    private final String homeUrl = BuildConfig.SITE_ORIGIN + "/demo";

    private ValueCallback<Uri[]> pendingFileCallback;
    private PermissionRequest pendingPermissionRequest;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(Color.WHITE);

        progress = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
        root.addView(progress, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 6));

        updateBanner = buildUpdateBanner();
        root.addView(updateBanner, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        updateBanner.setVisibility(View.GONE);

        errorPanel = new LinearLayout(this);
        errorPanel.setOrientation(LinearLayout.VERTICAL);
        errorPanel.setPadding(32, 32, 32, 32);
        TextView message = new TextView(this);
        message.setText(R.string.connection_error);
        message.setTextSize(16);
        errorPanel.addView(message);
        Button retry = new Button(this);
        retry.setText(R.string.retry);
        retry.setOnClickListener(new View.OnClickListener() {
            @Override public void onClick(View view) {
                errorPanel.setVisibility(View.GONE);
                webView.setVisibility(View.VISIBLE);
                webView.loadUrl(homeUrl);
            }
        });
        errorPanel.addView(retry);
        errorPanel.setVisibility(View.GONE);
        root.addView(errorPanel);

        webView = new WebView(this);
        WebSettings settings = webView.getSettings();
        settings.setJavaScriptEnabled(true);
        settings.setDomStorageEnabled(true);
        settings.setAllowFileAccess(false);
        settings.setAllowContentAccess(false);
        settings.setAllowUniversalAccessFromFileURLs(false);
        settings.setAllowFileAccessFromFileURLs(false);
        settings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
        settings.setGeolocationEnabled(false);
        settings.setSupportMultipleWindows(false);
        settings.setJavaScriptCanOpenWindowsAutomatically(false);
        settings.setUserAgentString(settings.getUserAgentString() + " DeepSeekNewAPIClient/" + BuildConfig.VERSION_NAME);
        webView.setDownloadListener(new android.webkit.DownloadListener() {
            @Override public void onDownloadStart(String url, String userAgent, String contentDisposition, String mimetype, long contentLength) {
                if (!policy.isAppDownload(url)) return;
                try { startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(url)).addCategory(Intent.CATEGORY_BROWSABLE)); }
                catch (ActivityNotFoundException ignored) { showError(); }
            }
        });
        if (Build.VERSION.SDK_INT >= 26) settings.setSafeBrowsingEnabled(true);
        CookieManager cookies = CookieManager.getInstance();
        cookies.setAcceptCookie(true);
        cookies.setAcceptThirdPartyCookies(webView, false);
        WebView.setWebContentsDebuggingEnabled(false);

        webView.setWebChromeClient(new WebChromeClient() {
            @Override public void onProgressChanged(WebView view, int value) {
                progress.setProgress(value);
                progress.setVisibility(value == 100 ? View.GONE : View.VISIBLE);
            }

            @Override public boolean onShowFileChooser(WebView view, ValueCallback<Uri[]> callback, FileChooserParams params) {
                // The page may be a redirect target or subframe; only the trusted
                // same-origin top-level page may open a picker.
                String pageUrl = view.getUrl();
                if (!FileChooserPolicy.mayShowForUrl(pageUrl, policy)) return false;
                if (pendingFileCallback != null) pendingFileCallback.onReceiveValue(null);
                pendingFileCallback = callback;
                String[] accept = FileChooserPolicy.normalizeAcceptTypes(params.getAcceptTypes());
                boolean multiple = FileChooserPolicy.allowMultiple(params.getMode() == FileChooserParams.MODE_OPEN_MULTIPLE, accept);
                Intent intent = new Intent(Intent.ACTION_GET_CONTENT);
                intent.addCategory(Intent.CATEGORY_OPENABLE);
                intent.setType(accept == null ? "*/*" : accept[0]);
                if (accept != null && accept.length > 1) intent.putExtra(Intent.EXTRA_MIME_TYPES, accept);
                if (multiple) intent.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true);
                try {
                    startActivityForResult(intent, REQUEST_CODE_FILE_CHOOSER);
                } catch (ActivityNotFoundException error) {
                    pendingFileCallback = null;
                    callback.onReceiveValue(null);
                    return false;
                }
                return true;
            }

            @Override public void onPermissionRequest(PermissionRequest request) {
                String pageUrl = webView == null ? null : webView.getUrl();
                String origin = request.getOrigin() == null ? null : request.getOrigin().toString();
                boolean runtimeGranted = checkSelfPermission(Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED;
                String[] granted = PermissionPolicy.decide(pageUrl, origin, request.getResources(), policy, runtimeGranted);
                if (granted.length > 0) {
                    request.grant(granted);
                    return;
                }
                // If the page wants audio but the user has not granted the runtime
                // permission, ask once with rationale; otherwise deny outright.
                if (PermissionPolicy.wantsAudio(request.getResources()) && policy.isTrusted(origin == null ? "" : origin) && !runtimeGranted) {
                    pendingPermissionRequest = request;
                    showMicRationale();
                    return;
                }
                request.deny();
            }
        });

        webView.setWebViewClient(new WebViewClient() {
            @Override public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                String url = request.getUrl().toString();
                if (policy.isTrusted(url)) return false;
                if (request.isForMainFrame() && request.hasGesture() && "https".equals(request.getUrl().getScheme())) {
                    try { startActivity(new Intent(Intent.ACTION_VIEW, request.getUrl()).addCategory(Intent.CATEGORY_BROWSABLE)); }
                    catch (ActivityNotFoundException ignored) { showError(); }
                }
                return true;
            }
            @Override public WebResourceResponse shouldInterceptRequest(WebView view, WebResourceRequest request) {
                if (!"GET".equals(request.getMethod())) return null;
                String asset = policy.assetPath(request.getUrl().toString(), request.isForMainFrame());
                if (asset == null) return null;
                String mime = MimeTypeMap.getSingleton().getMimeTypeFromExtension(MimeTypeMap.getFileExtensionFromUrl(asset));
                if (asset.endsWith(".js")) mime = "application/javascript";
                if (mime == null) mime = "application/octet-stream";
                try {
                    InputStream input = getAssets().open(asset);
                    return new WebResourceResponse(mime, "UTF-8", 200, "OK", Collections.singletonMap("Cache-Control", "no-store"), input);
                } catch (IOException missing) {
                    return new WebResourceResponse("text/plain", "UTF-8", 404, "Not Found", Collections.<String, String>emptyMap(), new ByteArrayInputStream(new byte[0]));
                }
            }
            @Override public void onReceivedError(WebView view, WebResourceRequest request, WebResourceError error) {
                if (request.isForMainFrame()) showError();
            }
            @Override public void onReceivedSslError(WebView view, SslErrorHandler handler, SslError error) {
                handler.cancel();
                showError();
            }
            @Override public void onPageFinished(WebView view, String url) {
                // The PermissionRequest grant is bound to the originating document;
                // once the main frame navigates, any in-flight grant must not be
                // replayed against the new page.
                if (pendingPermissionRequest != null) {
                    pendingPermissionRequest.deny();
                    pendingPermissionRequest = null;
                }
            }
            @Override public boolean onRenderProcessGone(WebView view, android.webkit.RenderProcessGoneDetail detail) {
                // A killed renderer must not silently replay a billable request.
                ViewGroup parent = (ViewGroup) view.getParent();
                if (parent != null) parent.removeView(view);
                view.destroy();
                webView = null;
                recreate();
                return true;
            }
        });
        root.addView(webView, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));
        setContentView(root);
        if (savedInstanceState == null || webView.restoreState(savedInstanceState) == null
                || !policy.isTrusted(webView.getUrl())) webView.loadUrl(homeUrl);

        maybeCheckForUpdate();
    }

    private LinearLayout buildUpdateBanner() {
        LinearLayout banner = new LinearLayout(this);
        banner.setOrientation(LinearLayout.HORIZONTAL);
        banner.setPadding(24, 12, 24, 12);
        banner.setBackgroundColor(Color.rgb(0xFF, 0xF4, 0xCE));
        updateBannerText = new TextView(this);
        updateBannerText.setText(R.string.update_banner_text);
        updateBannerText.setTextSize(14);
        updateBannerText.setTextColor(Color.rgb(0x33, 0x26, 0x00));
        banner.addView(updateBannerText, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        Button action = new Button(this);
        action.setText(R.string.update_banner_action);
        action.setOnClickListener(new View.OnClickListener() {
            @Override public void onClick(View view) {
                Uri target = Uri.parse(BuildConfig.SITE_ORIGIN + UPDATE_DOWNLOAD_PATH);
                try { startActivity(new Intent(Intent.ACTION_VIEW, target).addCategory(Intent.CATEGORY_BROWSABLE)); }
                catch (ActivityNotFoundException ignored) { /* nothing useful to do */ }
            }
        });
        banner.addView(action);
        return banner;
    }

    private void maybeCheckForUpdate() {
        SharedPreferences prefs = getSharedPreferences(PREFS_NAME, MODE_PRIVATE);
        long lastProbe = prefs.getLong(PREF_LAST_NEGATIVE_PROBE, 0L);
        if (UpdatePolicy.negativeCacheValid(lastProbe, System.currentTimeMillis())) return;
        new Thread(new Runnable() {
            @Override public void run() {
                String payload = null;
                try {
                    HttpURLConnection connection = (HttpURLConnection) new URL(BuildConfig.SITE_ORIGIN + "/api/status").openConnection();
                    connection.setConnectTimeout(4000);
                    connection.setReadTimeout(4000);
                    connection.setRequestProperty("Accept", "application/json");
                    connection.setRequestProperty("User-Agent", "DeepSeekNewAPIClient/" + BuildConfig.VERSION_NAME);
                    if (connection.getResponseCode() == 200) {
                        InputStream input = connection.getInputStream();
                        ByteArrayOutputStream buffer = new ByteArrayOutputStream();
                        byte[] chunk = new byte[4096];
                        int read;
                        int total = 0;
                        while ((read = input.read(chunk)) != -1 && total < 64 * 1024) {
                            buffer.write(chunk, 0, read);
                            total += read;
                        }
                        payload = new String(buffer.toByteArray(), StandardCharsets.UTF_8);
                    }
                    connection.disconnect();
                } catch (IOException ignored) {
                    payload = null;
                }
                final String body = payload;
                runOnUiThread(new Runnable() {
                    @Override public void run() {
                        if (isFinishing()) return;
                        UpdatePolicy.Decision decision = UpdatePolicy.evaluate(
                                body, BuildConfig.VERSION_NAME, BuildConfig.SITE_ORIGIN + UPDATE_DOWNLOAD_PATH);
                        if (decision.showBanner && updateBanner != null) {
                            updateBanner.setVisibility(View.VISIBLE);
                        } else {
                            getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                                    .edit()
                                    .putLong(PREF_LAST_NEGATIVE_PROBE, System.currentTimeMillis())
                                    .apply();
                        }
                    }
                });
            }
        }).start();
    }

    private void showMicRationale() {
        new AlertDialog.Builder(this)
                .setTitle(R.string.mic_rationale_title)
                .setMessage(R.string.mic_rationale_body)
                .setPositiveButton(R.string.mic_rationale_allow, (dialog, which) -> {
                    requestPermissions(new String[] { Manifest.permission.RECORD_AUDIO }, REQUEST_CODE_RECORD_AUDIO);
                })
                .setNegativeButton(R.string.mic_rationale_deny, (dialog, which) -> {
                    if (pendingPermissionRequest != null) {
                        pendingPermissionRequest.deny();
                        pendingPermissionRequest = null;
                    }
                })
                .setOnCancelListener(dialog -> {
                    if (pendingPermissionRequest != null) {
                        pendingPermissionRequest.deny();
                        pendingPermissionRequest = null;
                    }
                })
                .show();
    }

    @Override public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] grantResults) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults);
        if (requestCode != REQUEST_CODE_RECORD_AUDIO) return;
        boolean granted = grantResults.length > 0 && grantResults[0] == PackageManager.PERMISSION_GRANTED;
        if (pendingPermissionRequest == null) return;
        if (granted) {
            String pageUrl = webView == null ? null : webView.getUrl();
            String origin = pendingPermissionRequest.getOrigin() == null ? null : pendingPermissionRequest.getOrigin().toString();
            String[] resources = PermissionPolicy.decide(pageUrl, origin, pendingPermissionRequest.getResources(), policy, true);
            if (resources.length > 0) pendingPermissionRequest.grant(resources);
            else pendingPermissionRequest.deny();
        } else {
            pendingPermissionRequest.deny();
        }
        pendingPermissionRequest = null;
    }

    @Override protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode != REQUEST_CODE_FILE_CHOOSER || pendingFileCallback == null) return;
        if (resultCode != RESULT_OK || data == null) {
            pendingFileCallback.onReceiveValue(null);
            pendingFileCallback = null;
            return;
        }
        List<Uri> accepted = new ArrayList<>();
        if (data.getClipData() != null) {
            int count = data.getClipData().getItemCount();
            for (int i = 0; i < count; i++) {
                Uri candidate = data.getClipData().getItemAt(i).getUri();
                if (candidate != null && passesSizeCheck(candidate)) accepted.add(candidate);
            }
        } else if (data.getData() != null) {
            Uri candidate = data.getData();
            if (passesSizeCheck(candidate)) accepted.add(candidate);
        }
        pendingFileCallback.onReceiveValue(accepted.isEmpty() ? null : accepted.toArray(new Uri[0]));
        pendingFileCallback = null;
    }

    private boolean passesSizeCheck(Uri uri) {
        long size = -1L;
        try (Cursor cursor = getContentResolver().query(uri, null, null, null, null)) {
            if (cursor != null && cursor.moveToFirst()) {
                int column = cursor.getColumnIndex(OpenableColumns.SIZE);
                if (column >= 0 && !cursor.isNull(column)) size = cursor.getLong(column);
            }
        } catch (RuntimeException ignored) {
            size = -1L;
        }
        if (FileChooserPolicy.acceptSize(size)) return true;
        Toast.makeText(this, R.string.file_too_large, Toast.LENGTH_LONG).show();
        return false;
    }

    private void showError() {
        runOnUiThread(new Runnable() {
            @Override public void run() {
                if (isFinishing() || webView == null) return;
                progress.setVisibility(View.GONE);
                webView.setVisibility(View.GONE);
                errorPanel.setVisibility(View.VISIBLE);
            }
        });
    }

    @Override protected void onSaveInstanceState(Bundle state) {
        if (webView != null) webView.saveState(state);
        super.onSaveInstanceState(state);
    }
    @Override protected void onPause() {
        if (webView != null) {
            // Stop explicitly; never auto-retry an interrupted, potentially billable generation.
            webView.evaluateJavascript("window.dispatchEvent(new Event('pagehide'))", null);
            webView.onPause();
        }
        CookieManager.getInstance().flush();
        super.onPause();
    }
    @Override protected void onResume() {
        super.onResume();
        if (webView != null) webView.onResume();
    }
    @Override public void onBackPressed() {
        if (webView != null && webView.canGoBack()) webView.goBack();
        else super.onBackPressed();
    }
    @Override protected void onDestroy() {
        if (pendingFileCallback != null) {
            pendingFileCallback.onReceiveValue(null);
            pendingFileCallback = null;
        }
        if (pendingPermissionRequest != null) {
            pendingPermissionRequest.deny();
            pendingPermissionRequest = null;
        }
        if (webView != null) {
            ViewGroup parent = (ViewGroup) webView.getParent();
            if (parent != null) parent.removeView(webView);
            webView.destroy();
            webView = null;
        }
        super.onDestroy();
    }
}
