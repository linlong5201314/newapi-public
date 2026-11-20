package com.newapi.deepseek;

/**
 * Regression tests for the WebView PermissionRequest bridge. The bridge must
 * grant the microphone only when every one of these holds:
 *   1. The Android runtime permission RECORD_AUDIO was already granted.
 *   2. The visible top-level page is the trusted same-origin site.
 *   3. The PermissionRequest's own origin is the trusted same-origin site.
 *   4. The requested resource list actually contains AUDIO_CAPTURE.
 */
public final class PermissionPolicyTest {
    private static int assertions;
    private static void check(boolean condition, String message) {
        assertions++;
        if (!condition) throw new AssertionError(message);
    }

    public static void main(String[] args) {
        AppPolicy policy = new AppPolicy("https://gw.ailong.love");
        String sameOrigin = "https://gw.ailong.love";
        String pageUrl = "https://gw.ailong.love/demo";
        String[] audio = new String[] { PermissionPolicy.RESOURCE_AUDIO_CAPTURE };
        String[] video = new String[] { PermissionPolicy.RESOURCE_VIDEO_CAPTURE };
        String[] both = new String[] { PermissionPolicy.RESOURCE_VIDEO_CAPTURE, PermissionPolicy.RESOURCE_AUDIO_CAPTURE };

        // Happy path: same-origin + audio + runtime granted.
        String[] granted = PermissionPolicy.decide(pageUrl, sameOrigin, audio, policy, true);
        check(granted.length == 1 && PermissionPolicy.RESOURCE_AUDIO_CAPTURE.equals(granted[0]),
                "audio granted when same-origin + runtime granted");

        // Audio + video: only audio is granted.
        granted = PermissionPolicy.decide(pageUrl, sameOrigin, both, policy, true);
        check(granted.length == 1 && PermissionPolicy.RESOURCE_AUDIO_CAPTURE.equals(granted[0]),
                "video capture is stripped from a mixed request");

        // No runtime grant -> nothing.
        granted = PermissionPolicy.decide(pageUrl, sameOrigin, audio, policy, false);
        check(granted.length == 0, "runtime permission missing -> deny");

        // Untrusted page URL -> nothing.
        granted = PermissionPolicy.decide("https://attacker.example/", sameOrigin, audio, policy, true);
        check(granted.length == 0, "untrusted page URL -> deny");

        // Untrusted request origin -> nothing.
        granted = PermissionPolicy.decide(pageUrl, "https://attacker.example", audio, policy, true);
        check(granted.length == 0, "untrusted request origin -> deny");

        // Null origins -> nothing.
        granted = PermissionPolicy.decide(null, sameOrigin, audio, policy, true);
        check(granted.length == 0, "null page URL -> deny");
        granted = PermissionPolicy.decide(pageUrl, null, audio, policy, true);
        check(granted.length == 0, "null request origin -> deny");

        // Non-audio resources -> nothing.
        granted = PermissionPolicy.decide(pageUrl, sameOrigin, video, policy, true);
        check(granted.length == 0, "video-only request -> deny");
        granted = PermissionPolicy.decide(pageUrl, sameOrigin, new String[0], policy, true);
        check(granted.length == 0, "empty request -> deny");
        granted = PermissionPolicy.decide(pageUrl, sameOrigin, null, policy, true);
        check(granted.length == 0, "null request -> deny");

        // wantsAudio helper
        check(PermissionPolicy.wantsAudio(audio), "wantsAudio detects audio");
        check(PermissionPolicy.wantsAudio(both), "wantsAudio detects audio in mixed list");
        check(!PermissionPolicy.wantsAudio(video), "wantsAudio ignores video-only");
        check(!PermissionPolicy.wantsAudio(null), "wantsAudio handles null");
        check(!PermissionPolicy.wantsAudio(new String[0]), "wantsAudio handles empty");

        System.out.println("PermissionPolicy: " + assertions + " assertions passed");
    }
}
