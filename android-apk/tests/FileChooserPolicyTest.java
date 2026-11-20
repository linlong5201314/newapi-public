package com.newapi.deepseek;

/**
 * Regression tests for the WebView file chooser policy. These cover the
 * intent-construction decisions that the page's FileChooserParams drives:
 * accept-type normalization, multi-select opt-in, and oversize rejection.
 */
public final class FileChooserPolicyTest {
    private static int assertions;
    private static void check(boolean condition, String message) {
        assertions++;
        if (!condition) throw new AssertionError(message);
    }

    public static void main(String[] args) {
        AppPolicy policy = new AppPolicy("https://gw.ailong.love");

        // Accept-type normalization
        check(FileChooserPolicy.normalizeAcceptTypes(null) == null, "null accept array passes through");
        check(FileChooserPolicy.normalizeAcceptTypes(new String[0]) == null, "empty accept array passes through");
        check(FileChooserPolicy.normalizeAcceptTypes(new String[] {"", "  ", "\t"}) == null,
                "blank tokens collapse to null");
        String[] single = FileChooserPolicy.normalizeAcceptTypes(new String[] {"image/*"});
        check(single != null && single.length == 1 && "image/*".equals(single[0]),
                "single MIME type preserved");
        String[] multi = FileChooserPolicy.normalizeAcceptTypes(new String[] {"image/*", ".pdf", "image/*", "  image/png  "});
        check(multi != null && multi.length == 3, "duplicates and blanks removed");
        check("image/*".equals(multi[0]) && ".pdf".equals(multi[1]) && "image/png".equals(multi[2]),
                "order preserved after dedup");
        check(FileChooserPolicy.normalizeAcceptTypes(new String[] {"text/ht\tml"}) == null,
                "tokens with control characters rejected");
        check(FileChooserPolicy.normalizeAcceptTypes(new String[] {"text/html\r\nimage/png"}) == null,
                "tokens with embedded newlines rejected");

        // Multi-select gate
        check(!FileChooserPolicy.allowMultiple(false, new String[] {"image/*"}),
                "page must explicitly opt in to multiple selection");
        check(FileChooserPolicy.allowMultiple(true, new String[] {"image/*"}),
                "page opt-in enables multiple selection");
        check(FileChooserPolicy.allowMultiple(true, null),
                "multi-select still allowed when no accept filter is present");

        // Trusted-origin gate
        check(FileChooserPolicy.mayShowForUrl("https://gw.ailong.love/demo", policy),
                "same-origin page may show picker");
        check(!FileChooserPolicy.mayShowForUrl("https://attacker.example/demo", policy),
                "external page blocked from picker");
        check(!FileChooserPolicy.mayShowForUrl(null, policy),
                "missing page URL blocks picker");
        check(!FileChooserPolicy.mayShowForUrl("http://gw.ailong.love/demo", policy),
                "cleartext page blocked from picker");

        // Size cap
        check(FileChooserPolicy.acceptSize(-1L), "unknown size tolerated (SAF stream)");
        check(FileChooserPolicy.acceptSize(0L), "empty file tolerated");
        check(FileChooserPolicy.acceptSize(20L * 1024L * 1024L), "boundary 20 MiB accepted");
        check(!FileChooserPolicy.acceptSize(20L * 1024L * 1024L + 1L), "1 byte over limit rejected");
        check(!FileChooserPolicy.acceptSize(200L * 1024L * 1024L), "obvious oversize rejected");

        System.out.println("FileChooserPolicy: " + assertions + " assertions passed");
    }
}
