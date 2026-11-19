package com.newapi.deepseek;

public final class AppPolicyTest {
    private static int assertions;
    private static void check(boolean condition, String message) {
        assertions++;
        if (!condition) throw new AssertionError(message);
    }
    public static void main(String[] args) {
        AppPolicy policy = new AppPolicy("https://gw.ailong.love");
        check(policy.isTrusted("https://gw.ailong.love/demo"), "same-origin launch");
        check(policy.isTrusted("https://gw.ailong.love:443/login?redirect=%2Fdemo"), "same-origin session redirect");
        check(!policy.isTrusted("http://gw.ailong.love/demo"), "reject cleartext");
        check(!policy.isTrusted("https://gw.ailong.love.attacker.example/demo"), "reject lookalike host");
        check(!policy.isTrusted("https://attacker@gw.ailong.love/demo"), "reject URL credentials");
        check(!policy.isTrusted("https://gw.ailong.love:8443/demo"), "reject unexpected port");
        check(!policy.isTrusted("intent://gw.ailong.love/demo"), "reject intent scheme");
        check(!policy.isTrusted("file:///android_asset/index.html"), "reject file origin");
        check("www/index.html".equals(policy.assetPath("https://gw.ailong.love/demo", true)), "bundled chat route");
        check("www/index.html".equals(policy.assetPath("https://gw.ailong.love/login?redirect=%2Fdemo", true)), "bundled login route");
        check("www/static/js/index.js".equals(policy.assetPath("https://gw.ailong.love/static/js/index.js", false)), "bundled script");
        check("www/index.html".equals(policy.assetPath("https://gw.ailong.love/console/my-channels", true)), "own channels route");
        check("www/index.html".equals(policy.assetPath("https://gw.ailong.love/console/public-pool", true)), "public pool route");
        check(policy.assetPath("https://gw.ailong.love/deepseek-newapi.apk", true) == null, "app download must use network, not absent bundled APK");
        check(policy.assetPath("https://gw.ailong.love/deepseek-newapi.apk?v=9", false) == null, "versioned app download must use network");
        check(policy.isAppDownload("https://gw.ailong.love/deepseek-newapi.apk"), "trusted app update download");
        check(policy.isAppDownload("https://gw.ailong.love/deepseek-newapi.apk?v=9"), "trusted versioned app update download");
        for (String url : new String[] {"https://evil.example/deepseek-newapi.apk", "http://gw.ailong.love/deepseek-newapi.apk", "https://gw.ailong.love:8443/deepseek-newapi.apk", "https://user@gw.ailong.love/deepseek-newapi.apk", "https://gw.ailong.love/other.apk", "https://gw.ailong.love/deepseek-newapi.apk/extra", "https://gw.ailong.love/demo", null}) {
            check(!policy.isAppDownload(url), "reject non-update download: " + url);
        }
        for (String path : new String[] {"/api/user/self", "/api/user/login", "/api/auth/refresh", "/pg/chat/completions", "/v1/models", "/oauth/github"}) {
            check(policy.assetPath("https://gw.ailong.love" + path, true) == null, "network passthrough: " + path);
        }
        check(policy.assetPath("https://gw.ailong.love/%2e%2e/private.txt", false) == null, "reject encoded traversal");
        check(policy.assetPath("https://gw.ailong.love/assets/%5csecret", false) == null, "reject encoded backslash");
        check(policy.assetPath("https://evil.example/demo", true) == null, "never inject local assets into external origin");
        boolean rejected = false;
        try { new AppPolicy("http://gw.ailong.love"); } catch (IllegalArgumentException expected) { rejected = true; }
        check(rejected, "insecure build origin rejected");
        System.out.println("AppPolicy: " + assertions + " assertions passed");
    }
}
