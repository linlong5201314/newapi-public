package com.newapi.deepseek;

import java.net.URI;
import java.net.URISyntaxException;
import java.util.ArrayList;
import java.util.List;

/**
 * Pure-Java decision logic for the WebView file chooser bridge.
 *
 * The system SAF picker enforces its own UI, so this class only normalizes the
 * accept-types list, decides whether multi-select is allowed from the page's
 * {@code FileChooserParams}, and rejects oversize picks before they are handed
 * back to the page. Kept Android-free so the rules can be exercised by the
 * host-side regression tests with plain javac/java.
 */
public final class FileChooserPolicy {
    /** Hard cap on a single picked file. The web composer must not accept more. */
    public static final long MAX_SINGLE_FILE_BYTES = 20L * 1024L * 1024L;

    private FileChooserPolicy() { /* no instances */ }

    /**
     * Collapse the accept-types array coming from the page into the safe list
     * of MIME types passed to {@code Intent#setType} / {@code EXTRA_MIME_TYPES}.
     *
     * Returns {@code null} when the page did not request any filter (i.e. the
     * picker should default to {@code *}/{@code *}). Empty strings, blanks and
     * obviously malformed tokens are dropped; non-MIME extensions such as
     * {@code .jpg} are kept verbatim because the Storage Access Framework
     * accepts them in {@code EXTRA_MIME_TYPES} on modern Android.
     */
    public static String[] normalizeAcceptTypes(String[] raw) {
        if (raw == null || raw.length == 0) return null;
        List<String> accepted = new ArrayList<>();
        for (String token : raw) {
            if (token == null) continue;
            String trimmed = token.trim();
            if (trimmed.isEmpty()) continue;
            // Reject any token whose trimmed form still contains characters
            // outside the printable ASCII set. MIME types and `.ext`
            // extensions are pure ASCII; whitespace, control characters, or
            // quotes inside the token would confuse the intent resolver and
            // are a sign of a malformed page-supplied filter.
            boolean safe = true;
            for (int i = 0; i < trimmed.length(); i++) {
                char c = trimmed.charAt(i);
                if (c < 0x21 || c > 0x7E) { safe = false; break; }
            }
            if (!safe) continue;
            if (!accepted.contains(trimmed)) accepted.add(trimmed);
        }
        if (accepted.isEmpty()) return null;
        return accepted.toArray(new String[0]);
    }

    /**
     * Whether the page's {@code <input type="file" multiple>} flag should be
     * honored. We refuse multi-select whenever the page did not explicitly ask
     * for it, so a hostile or buggy page cannot force a bulk picker.
     */
    public static boolean allowMultiple(boolean pageRequestedMultiple, String[] normalizedAccept) {
        return pageRequestedMultiple; // normalizedAccept kept for future tightening
    }

    /**
     * Whether the picker may be opened for the supplied page URL at all. We only
     * let the same-origin site trigger the picker; if the WebView ever loads a
     * third-party page that manages to invoke {@code onShowFileChooser}, the
     * request is denied by the caller before any picker is shown.
     */
    public static boolean mayShowForUrl(String pageUrl, AppPolicy policy) {
        return pageUrl != null && policy.isTrusted(pageUrl);
    }

    /**
     * Whether a returned Uri is small enough to forward to the page. Callers
     * pass {@code -1} for unknown sizes; unknown sizes are accepted because the
     * SAF contract does not guarantee a size for streaming providers, and the
     * web side applies its own limit when uploading.
     */
    public static boolean acceptSize(long sizeBytes) {
        return sizeBytes < 0 || sizeBytes <= MAX_SINGLE_FILE_BYTES;
    }
}
