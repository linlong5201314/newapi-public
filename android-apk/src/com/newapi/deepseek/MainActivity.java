package com.newapi.deepseek;

import android.app.Activity;
import android.content.ActivityNotFoundException;
import android.content.Intent;
import android.graphics.Color;
import android.net.Uri;
import android.net.http.SslError;
import android.os.Build;
import android.os.Bundle;
import android.view.View;
import android.view.ViewGroup;
import android.webkit.CookieManager;
import android.webkit.MimeTypeMap;
import android.webkit.SslErrorHandler;
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
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.util.Collections;

public class MainActivity extends Activity {
    private WebView webView;
    private LinearLayout errorPanel;
    private ProgressBar progress;
    private final AppPolicy policy = new AppPolicy(BuildConfig.SITE_ORIGIN);
    private final String homeUrl = BuildConfig.SITE_ORIGIN + "/demo";

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(Color.WHITE);
        progress = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
        root.addView(progress, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 6));
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
        settings.setUserAgentString(settings.getUserAgentString() + " DeepSeekNewAPIClient/1.1.0");
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
        if (webView != null) {
            ViewGroup parent = (ViewGroup) webView.getParent();
            if (parent != null) parent.removeView(webView);
            webView.destroy();
            webView = null;
        }
        super.onDestroy();
    }
}
