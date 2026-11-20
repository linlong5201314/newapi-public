package com.newapi.deepseek;

/**
 * Regression tests for the in-app update-check parser and decision logic.
 *
 * The gateway's /api/status payload currently exposes only the server-side
 * semantic version (data.version); it does NOT carry an Android APK version.
 * Until a dedicated `android_apk_version` field is added on the backend, the
 * banner is inert and these tests lock in that behavior so a future field
 * roll-out can be detected by flipping a single assertion.
 */
public final class UpdatePolicyTest {
    private static int assertions;
    private static void check(boolean condition, String message) {
        assertions++;
        if (!condition) throw new AssertionError(message);
    }

    public static void main(String[] args) {
        String embedded = "1.2.6-browser";
        String download = "https://gw.ailong.love/deepseek-newapi.apk";

        // Inert default: no field at all -> never show.
        UpdatePolicy.Decision decision = UpdatePolicy.evaluate(
                "{\"success\":true,\"data\":{\"version\":\"0.9.1\"}}", embedded, download);
        check(!decision.showBanner, "missing android_apk_version field -> inert");
        check(decision.advertisedVersion == null, "no advertised version when field is absent");

        // Live-server payload shape (real /api/status): banner must stay hidden.
        decision = UpdatePolicy.evaluate(
                "{\"success\":true,\"data\":{\"version\":\"\",\"start_time\":1795158318}}",
                embedded, download);
        check(!decision.showBanner, "empty server version field does not trigger banner");

        // Null / malformed input is inert, never throws.
        check(!UpdatePolicy.evaluate(null, embedded, download).showBanner, "null body inert");
        check(!UpdatePolicy.evaluate("", embedded, download).showBanner, "empty body inert");
        check(!UpdatePolicy.evaluate("not json", embedded, download).showBanner, "non-JSON inert");
        check(!UpdatePolicy.evaluate("{\"data\":{\"android_apk_version\":123}}", embedded, download).showBanner,
                "non-string field inert");
        check(!UpdatePolicy.evaluate("{\"data\":{\"android_apk_version\":\"1.2", embedded, download).showBanner,
                "truncated JSON inert");

        // Well-formed advertisement -> show only when strictly newer.
        decision = UpdatePolicy.evaluate(
                "{\"success\":true,\"data\":{\"android_apk_version\":\"1.2.7\"}}",
                embedded, download);
        check(decision.showBanner, "newer advertised APK version shows banner");
        check("1.2.7".equals(decision.advertisedVersion), "advertised version surfaced");
        check(download.equals(decision.downloadUrl), "download URL surfaced");

        // Equal or older -> stay hidden.
        check(!UpdatePolicy.evaluate(
                "{\"data\":{\"android_apk_version\":\"1.2.6\"}}", embedded, download).showBanner,
                "equal version hidden");
        check(!UpdatePolicy.evaluate(
                "{\"data\":{\"android_apk_version\":\"1.2.5\"}}", embedded, download).showBanner,
                "older version hidden");

        // Suffix handling: 1.2.6-browser == 1.2.6 numerically.
        check(!UpdatePolicy.evaluate(
                "{\"data\":{\"android_apk_version\":\"1.2.6\"}}", "1.2.6-browser", download).showBanner,
                "suffix on embedded build does not pretend to be older");
        check(UpdatePolicy.evaluate(
                "{\"data\":{\"android_apk_version\":\"1.2.7\"}}", "1.2.6-browser", download).showBanner,
                "suffix on embedded build still detects newer advertised version");

        // Malformed advertised versions are rejected.
        for (String bad : new String[] {"", "abc", "1.2.x", "1..2", "v1.2.3", "1.2.3.4.5"}) {
            check(!UpdatePolicy.evaluate(
                    "{\"data\":{\"android_apk_version\":\"" + bad + "\"}}", embedded, download).showBanner,
                    "reject malformed advertised version: " + bad);
        }

        // Numeric comparison is lexicographic-by-segment, not string order.
        check(UpdatePolicy.evaluate(
                "{\"data\":{\"android_apk_version\":\"1.10.0\"}}", "1.9.9", download).showBanner,
                "1.10.0 > 1.9.9 -> banner shown");

        // parseVersion / compareVersions unit surface
        check(UpdatePolicy.parseVersion("1.2.3") != null, "parseVersion accepts 1.2.3");
        check(UpdatePolicy.parseVersion("1") != null, "parseVersion accepts 1");
        check(UpdatePolicy.parseVersion("1.2.3.4") != null, "parseVersion accepts 1.2.3.4");
        check(UpdatePolicy.parseVersion("1.2.3.4.5") == null, "parseVersion rejects 5 segments");
        check(UpdatePolicy.parseVersion("") == null, "parseVersion rejects empty");
        check(UpdatePolicy.parseVersion(null) == null, "parseVersion rejects null");
        check(UpdatePolicy.compareVersions(new int[] {1, 2}, new int[] {1, 2, 0}) == 0,
                "trailing zero segments equal");
        check(UpdatePolicy.compareVersions(new int[] {1, 10}, new int[] {1, 9}) > 0,
                "segment comparison is numeric, not lexicographic");

        // Negative-cache TTL
        long now = System.currentTimeMillis();
        check(!UpdatePolicy.negativeCacheValid(0L, now), "zero timestamp -> expired");
        check(!UpdatePolicy.negativeCacheValid(-1L, now), "negative timestamp -> expired");
        check(UpdatePolicy.negativeCacheValid(now - 1000L, now), "recent probe still fresh");
        check(UpdatePolicy.negativeCacheValid(now - (6L * 60L * 60L * 1000L) + 1000L, now),
                "just under 6h still fresh");
        check(!UpdatePolicy.negativeCacheValid(now - (6L * 60L * 60L * 1000L) - 1000L, now),
                "past 6h expired");
        check(!UpdatePolicy.negativeCacheValid(now + 60_000L, now),
                "clock-moved-backwards treated as expired");

        System.out.println("UpdatePolicy: " + assertions + " assertions passed");
    }
}
