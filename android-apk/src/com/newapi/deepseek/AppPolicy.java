package com.newapi.deepseek;

import java.net.URI;
import java.net.URISyntaxException;

/** Pure Java policy shared by the WebView and host-side regression tests. */
public final class AppPolicy {
    private final URI origin;

    public AppPolicy(String siteOrigin) {
        try {
            origin = new URI(siteOrigin);
        } catch (URISyntaxException error) {
            throw new IllegalArgumentException("Invalid site origin", error);
        }
        if (!"https".equals(origin.getScheme()) || origin.getHost() == null
                || origin.getUserInfo() != null || origin.getQuery() != null
                || origin.getFragment() != null
                || !(origin.getPath().isEmpty() || "/".equals(origin.getPath()))) {
            throw new IllegalArgumentException("Site origin must be HTTPS without credentials or a path");
        }
    }

    public boolean isTrusted(String url) {
        try {
            URI candidate = new URI(url);
            return "https".equals(candidate.getScheme())
                    && origin.getHost().equalsIgnoreCase(candidate.getHost())
                    && port(origin) == port(candidate) && candidate.getUserInfo() == null;
        } catch (URISyntaxException | NullPointerException error) {
            return false;
        }
    }

    public boolean isAppDownload(String url) {
        if (!isTrusted(url)) return false;
        try {
            return "/deepseek-newapi.apk".equals(new URI(url).getPath());
        } catch (URISyntaxException error) {
            return false;
        }
    }

    public String assetPath(String url, boolean mainFrame) {
        if (!isTrusted(url) || isAppDownload(url)) return null;
        try {
            String path = new URI(url).getPath();
            if (path == null || path.contains("\\") || path.contains("\u0000")) return null;
            for (String segment : path.split("/")) {
                if (segment.equals("..") || segment.equals(".")) return null;
            }
            for (String prefix : new String[] {"/api", "/pg", "/v1", "/mj", "/oauth"}) {
                if (path.equals(prefix) || path.startsWith(prefix + "/")) return null;
            }
            if (path.equals("/") || (mainFrame && !path.substring(path.lastIndexOf('/') + 1).contains("."))) {
                return "www/index.html";
            }
            return "www/" + path.substring(1);
        } catch (URISyntaxException error) {
            return null;
        }
    }

    private static int port(URI uri) {
        return uri.getPort() == -1 ? 443 : uri.getPort();
    }
}
