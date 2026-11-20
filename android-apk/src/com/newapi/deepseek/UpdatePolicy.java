package com.newapi.deepseek;

/**
 * Pure-Java decision logic for the in-app update banner.
 *
 * The gateway's {@code GET /api/status} payload carries the server's own
 * semantic version in {@code data.version}; it does NOT advertise an Android
 * client build. This class therefore implements an explicit opt-in contract:
 * the banner is shown only when the status payload contains a dedicated
 * {@code data.android_apk_version} field that is a well-formed {@code X.Y.Z}
 * (or {@code X.Y.Z-suffix}) version strictly newer than the embedded
 * {@code BuildConfig.VERSION_NAME}. Until the backend ships that field, the
 * check is inert and the banner never appears — we deliberately do NOT
 * piggyback on the server version because it has no relation to the APK
 * release cadence and would either spam users or stall them on a stale build.
 *
 * A negative result (no update, network failure, malformed payload) is cached
 * for {@link #NEGATIVE_CACHE_TTL_MILLIS} so a flaky connection does not cause
 * a request on every launch.
 */
public final class UpdatePolicy {
    public static final long NEGATIVE_CACHE_TTL_MILLIS = 6L * 60L * 60L * 1000L;
    /** JSON field the backend may add to {@code /api/status} to advertise an APK version. */
    public static final String FIELD_ANDROID_APK_VERSION = "android_apk_version";

    private UpdatePolicy() { /* no instances */ }

    /** Result of a single update probe. */
    public static final class Decision {
        public final boolean showBanner;
        public final String advertisedVersion;
        public final String downloadUrl;

        Decision(boolean showBanner, String advertisedVersion, String downloadUrl) {
            this.showBanner = showBanner;
            this.advertisedVersion = advertisedVersion;
            this.downloadUrl = downloadUrl;
        }
    }

    /**
     * Decide whether to show the banner given the raw {@code /api/status}
     * payload, the embedded APK versionName, and the same-origin download URL.
     *
     * This method never throws on malformed input; any parse failure yields
     * {@code showBanner=false} so the launch path is never blocked.
     */
    public static Decision evaluate(String statusJson, String embeddedVersionName, String downloadUrl) {
        if (statusJson == null || embeddedVersionName == null || downloadUrl == null) {
            return new Decision(false, null, null);
        }
        String advertised = extractField(statusJson, FIELD_ANDROID_APK_VERSION);
        if (advertised == null || advertised.isEmpty()) {
            return new Decision(false, null, null);
        }
        int[] advertisedParts = parseVersion(advertised);
        int[] embeddedParts = parseVersion(stripSuffix(embeddedVersionName));
        if (advertisedParts == null || embeddedParts == null) {
            return new Decision(false, null, null);
        }
        if (compareVersions(advertisedParts, embeddedParts) <= 0) {
            return new Decision(false, advertised, null);
        }
        return new Decision(true, advertised, downloadUrl);
    }

    /**
     * Pull a top-level string field out of the {@code data} object of the
     * status payload. We deliberately do NOT bundle a JSON parser; this is a
     * minimal, allocation-light scan that tolerates whitespace and ordering
     * but rejects anything other than a plain {@code "name":"value"} string.
     */
    static String extractField(String json, String field) {
        if (json == null || field == null) return null;
        String needle = "\"" + field + "\"";
        int index = json.indexOf(needle);
        if (index < 0) return null;
        int cursor = index + needle.length();
        while (cursor < json.length() && Character.isWhitespace(json.charAt(cursor))) cursor++;
        if (cursor >= json.length() || json.charAt(cursor) != ':') return null;
        cursor++;
        while (cursor < json.length() && Character.isWhitespace(json.charAt(cursor))) cursor++;
        if (cursor >= json.length() || json.charAt(cursor) != '"') return null;
        int start = ++cursor;
        StringBuilder value = new StringBuilder();
        while (cursor < json.length()) {
            char c = json.charAt(cursor);
            if (c == '\\' && cursor + 1 < json.length()) {
                // Only tolerate \" and \\ inside the value; anything fancier
                // is treated as not-a-version and rejected by parseVersion.
                char next = json.charAt(cursor + 1);
                if (next == '"' || next == '\\') {
                    value.append(next);
                    cursor += 2;
                    continue;
                }
                return null;
            }
            if (c == '"') return value.toString();
            value.append(c);
            cursor++;
        }
        return null;
    }

    /**
     * Strip a {@code -suffix} (e.g. {@code 1.2.6-browser} → {@code 1.2.6}).
     * Versions whose numeric core cannot be parsed are rejected by
     * {@link #parseVersion}.
     */
    static String stripSuffix(String versionName) {
        if (versionName == null) return null;
        int dash = versionName.indexOf('-');
        return dash < 0 ? versionName : versionName.substring(0, dash);
    }

    /**
     * Parse a strict {@code X.Y.Z} (or {@code X.Y} / {@code X}) version into
     * its numeric components. Non-numeric segments yield {@code null}.
     */
    static int[] parseVersion(String version) {
        if (version == null || version.isEmpty()) return null;
        String[] parts = version.split("\\.");
        if (parts.length == 0 || parts.length > 4) return null;
        int[] out = new int[parts.length];
        for (int i = 0; i < parts.length; i++) {
            String piece = parts[i];
            if (piece.isEmpty() || piece.length() > 9) return null;
            int value = 0;
            for (int j = 0; j < piece.length(); j++) {
                char c = piece.charAt(j);
                if (c < '0' || c > '9') return null;
                value = value * 10 + (c - '0');
            }
            out[i] = value;
        }
        return out;
    }

    /**
     * Lexicographic numeric comparison. Missing trailing components count as
     * zero, so {@code 1.2} equals {@code 1.2.0}.
     */
    static int compareVersions(int[] a, int[] b) {
        int length = Math.max(a.length, b.length);
        for (int i = 0; i < length; i++) {
            int left = i < a.length ? a[i] : 0;
            int right = i < b.length ? b[i] : 0;
            if (left != right) return left < right ? -1 : 1;
        }
        return 0;
    }

    /**
     * Whether the previous negative probe is still fresh enough to skip the
     * network call this launch.
     */
    public static boolean negativeCacheValid(long lastProbeMillis, long nowMillis) {
        if (lastProbeMillis <= 0) return false;
        if (nowMillis < lastProbeMillis) return false; // clock moved backwards; do not trust
        return (nowMillis - lastProbeMillis) < NEGATIVE_CACHE_TTL_MILLIS;
    }
}
