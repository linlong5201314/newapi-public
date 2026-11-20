package com.newapi.deepseek;

import java.net.URI;
import java.net.URISyntaxException;

/**
 * Pure-Java decision logic for the WebView {@code PermissionRequest} bridge.
 *
 * The WebView may ask for {@code RESOURCE_AUDIO_CAPTURE}, {@code RESOURCE_VIDEO_CAPTURE},
 * {@code RESOURCE_MIDI_SYSEX}, etc. This client only ever grants audio capture,
 * only for the embedded same-origin site, and only after the user has already
 * approved the Android runtime RECORD_AUDIO permission. Everything else is
 * denied. The grant decision is per-request and is not persisted across page
 * loads; the caller clears any in-flight grants when the main frame navigates.
 */
public final class PermissionPolicy {
    /** Mirrors {@code PermissionRequest.RESOURCE_AUDIO_CAPTURE} without an Android dependency. */
    public static final String RESOURCE_AUDIO_CAPTURE = "android.webkit.resource.AUDIO_CAPTURE";
    /** Mirrors {@code PermissionRequest.RESOURCE_VIDEO_CAPTURE}. */
    public static final String RESOURCE_VIDEO_CAPTURE = "android.webkit.resource.VIDEO_CAPTURE";

    private PermissionPolicy() { /* no instances */ }

    /**
     * Decide which resources from a PermissionRequest should be granted.
     *
     * @param pageUrl     top-level page URL that originated the request
     * @param origin      the origin reported by the PermissionRequest itself
     * @param requested   resources listed by {@code PermissionRequest#getResources()}
     * @param policy      the trusted-origin policy
     * @param runtimeGranted whether the Android runtime RECORD_AUDIO permission
     *                       has already been granted by the user
     * @return the (possibly empty) list of resources that may be granted.
     *         An empty list means the caller must call {@code request.deny()}.
     */
    public static String[] decide(String pageUrl, String origin, String[] requested,
                                  AppPolicy policy, boolean runtimeGranted) {
        if (requested == null || requested.length == 0) return new String[0];
        if (policy == null || !runtimeGranted) return new String[0];
        // Both the visible page URL and the request's own origin must be the
        // trusted site. An iframe or a redirected page must not silently gain
        // the microphone on the strength of a same-origin top-level page.
        if (pageUrl == null || !policy.isTrusted(pageUrl)) return new String[0];
        if (origin == null || !policy.isTrusted(origin)) return new String[0];
        for (String resource : requested) {
            if (RESOURCE_AUDIO_CAPTURE.equals(resource)) {
                return new String[] { RESOURCE_AUDIO_CAPTURE };
            }
        }
        return new String[0];
    }

    /**
     * Convenience predicate: should this resource be granted for a microphone
     * tap? Used by the in-app rationale dialog to decide whether a runtime
     * RECORD_AUDIO prompt is needed before the WebView grant.
     */
    public static boolean wantsAudio(String[] requested) {
        if (requested == null) return false;
        for (String resource : requested) {
            if (RESOURCE_AUDIO_CAPTURE.equals(resource)) return true;
        }
        return false;
    }
}
