# -*- coding: utf-8 -*-
"""
Deterministic Dalvik DEX (035) assembler for com.newapi.deepseek.MainActivity.
Verified by Android SDK build-tools/34.0.0/dexdump.exe -d -f -h.
"""
import hashlib
import struct
import sys
import zlib


def uleb128(val: int) -> bytes:
    out = bytearray()
    while True:
        b = val & 0x7F
        val >>= 7
        if val != 0:
            b |= 0x80
            out.append(b)
        else:
            out.append(b)
            break
    return bytes(out)


def mutf8_string_data(s: str) -> bytes:
    raw = s.encode("utf-8")
    return uleb128(len(s)) + raw + b"\x00"


def align4(buf: bytearray):
    while len(buf) % 4 != 0:
        buf.append(0)


def build_dex(output_path: str):
    strings = sorted([
        "<init>",
        "L",
        "Landroid/app/Activity;",
        "Landroid/content/Context;",
        "Landroid/os/Bundle;",
        "Landroid/view/View;",
        "Landroid/webkit/WebChromeClient;",
        "Landroid/webkit/WebSettings;",
        "Landroid/webkit/WebView;",
        "Landroid/webkit/WebViewClient;",
        "Lcom/newapi/deepseek/MainActivity;",
        "Ljava/lang/String;",
        "MainActivity.java",
        "V",
        "VL",
        "VZ",
        "Z",
        "file:///android_asset/index.html",
        "getSettings",
        "loadUrl",
        "onCreate",
        "setContentView",
        "setDomStorageEnabled",
        "setJavaScriptEnabled",
        "setWebChromeClient",
        "setWebViewClient",
    ])
    str_idx = {s: i for i, s in enumerate(strings)}

    types = sorted([
        "Landroid/app/Activity;",
        "Landroid/content/Context;",
        "Landroid/os/Bundle;",
        "Landroid/view/View;",
        "Landroid/webkit/WebChromeClient;",
        "Landroid/webkit/WebSettings;",
        "Landroid/webkit/WebView;",
        "Landroid/webkit/WebViewClient;",
        "Lcom/newapi/deepseek/MainActivity;",
        "Ljava/lang/String;",
        "V",
        "Z",
    ], key=lambda t: str_idx[t])
    type_idx = {t: i for i, t in enumerate(types)}

    raw_protos = [
        ("V", "V", ()),
        ("VL", "V", ("Landroid/content/Context;",)),
        ("VL", "V", ("Landroid/os/Bundle;",)),
        ("VL", "V", ("Landroid/view/View;",)),
        ("VL", "V", ("Landroid/webkit/WebChromeClient;",)),
        ("VL", "V", ("Landroid/webkit/WebViewClient;",)),
        ("VL", "V", ("Ljava/lang/String;",)),
        ("VZ", "V", ("Z",)),
        ("L", "Landroid/webkit/WebSettings;", ()),
    ]
    protos = sorted(
        raw_protos,
        key=lambda p: (type_idx[p[1]], [type_idx[x] for x in p[2]])
    )
    proto_idx = {(p[1], p[2]): i for i, p in enumerate(protos)}

    raw_methods = [
        ("Landroid/app/Activity;", ("V", ()), "<init>"),
        ("Landroid/app/Activity;", ("V", ("Landroid/os/Bundle;",)), "onCreate"),
        ("Landroid/app/Activity;", ("V", ("Landroid/view/View;",)), "setContentView"),
        ("Landroid/webkit/WebChromeClient;", ("V", ()), "<init>"),
        ("Landroid/webkit/WebSettings;", ("V", ("Z",)), "setDomStorageEnabled"),
        ("Landroid/webkit/WebSettings;", ("V", ("Z",)), "setJavaScriptEnabled"),
        ("Landroid/webkit/WebView;", ("V", ("Landroid/content/Context;",)), "<init>"),
        ("Landroid/webkit/WebView;", ("Landroid/webkit/WebSettings;", ()), "getSettings"),
        ("Landroid/webkit/WebView;", ("V", ("Ljava/lang/String;",)), "loadUrl"),
        ("Landroid/webkit/WebView;", ("V", ("Landroid/webkit/WebChromeClient;",)), "setWebChromeClient"),
        ("Landroid/webkit/WebView;", ("V", ("Landroid/webkit/WebViewClient;",)), "setWebViewClient"),
        ("Landroid/webkit/WebViewClient;", ("V", ()), "<init>"),
        ("Lcom/newapi/deepseek/MainActivity;", ("V", ()), "<init>"),
        ("Lcom/newapi/deepseek/MainActivity;", ("V", ("Landroid/os/Bundle;",)), "onCreate"),
    ]
    methods = sorted(
        raw_methods,
        key=lambda m: (type_idx[m[0]], str_idx[m[2]], proto_idx[m[1]])
    )
    method_idx = {(m[0], m[2], m[1]): i for i, m in enumerate(methods)}

    # Assemble Dalvik bytecode for MainActivity.<init>()V
    # registers_size=1, ins_size=1, outs_size=1, tries_size=0
    # 7010 <Activity.<init>> 0000 ; invoke-direct {v0}, Landroid/app/Activity;-><init>()V
    # 0e00                        ; return-void
    m_act_init = method_idx[("Landroid/app/Activity;", "<init>", ("V", ()))]
    insns_init = struct.pack("<HHH", 0x1070, m_act_init, 0x0000) + struct.pack("<H", 0x000E)
    code_init = struct.pack("<HHHHII", 1, 1, 1, 0, 0, len(insns_init) // 2) + insns_init

    # Assemble Dalvik bytecode for MainActivity.onCreate(Bundle)V
    # registers_size=5 (v0=webView, v1=settings, v2=tmp, v3=this, v4=savedInstanceState), ins_size=2, outs_size=2
    m_super_oncreate = method_idx[("Landroid/app/Activity;", "onCreate", ("V", ("Landroid/os/Bundle;",)))]
    m_wv_init = method_idx[("Landroid/webkit/WebView;", "<init>", ("V", ("Landroid/content/Context;",)))]
    m_get_settings = method_idx[("Landroid/webkit/WebView;", "getSettings", ("Landroid/webkit/WebSettings;", ()))]
    m_set_js = method_idx[("Landroid/webkit/WebSettings;", "setJavaScriptEnabled", ("V", ("Z",)))]
    m_set_dom = method_idx[("Landroid/webkit/WebSettings;", "setDomStorageEnabled", ("V", ("Z",)))]
    m_wcc_init = method_idx[("Landroid/webkit/WebChromeClient;", "<init>", ("V", ()))]
    m_set_wcc = method_idx[("Landroid/webkit/WebView;", "setWebChromeClient", ("V", ("Landroid/webkit/WebChromeClient;",)))]
    m_wvc_init = method_idx[("Landroid/webkit/WebViewClient;", "<init>", ("V", ()))]
    m_set_wvc = method_idx[("Landroid/webkit/WebView;", "setWebViewClient", ("V", ("Landroid/webkit/WebViewClient;",)))]
    m_set_content = method_idx[("Landroid/app/Activity;", "setContentView", ("V", ("Landroid/view/View;",)))]
    m_load_url = method_idx[("Landroid/webkit/WebView;", "loadUrl", ("V", ("Ljava/lang/String;",)))]

    t_webview = type_idx["Landroid/webkit/WebView;"]
    t_wcc = type_idx["Landroid/webkit/WebChromeClient;"]
    t_wvc = type_idx["Landroid/webkit/WebViewClient;"]
    s_url = str_idx["file:///android_asset/index.html"]

    insns_oncreate = b"".join([
        # invoke-super {v3, v4}, Landroid/app/Activity;->onCreate(Landroid/os/Bundle;)V
        struct.pack("<HHH", 0x206F, m_super_oncreate, 0x0043),
        # new-instance v0, Landroid/webkit/WebView;
        struct.pack("<HH", 0x0022, t_webview),
        # invoke-direct {v0, v3}, Landroid/webkit/WebView;-><init>(Landroid/content/Context;)V
        struct.pack("<HHH", 0x2070, m_wv_init, 0x0030),
        # invoke-virtual {v0}, Landroid/webkit/WebView;->getSettings()Landroid/webkit/WebSettings;
        struct.pack("<HHH", 0x106E, m_get_settings, 0x0000),
        # move-result-object v1
        struct.pack("<H", 0x010C),
        # const/4 v2, #int 1
        struct.pack("<H", 0x1212),
        # invoke-virtual {v1, v2}, Landroid/webkit/WebSettings;->setJavaScriptEnabled(Z)V
        struct.pack("<HHH", 0x206E, m_set_js, 0x0021),
        # invoke-virtual {v1, v2}, Landroid/webkit/WebSettings;->setDomStorageEnabled(Z)V
        struct.pack("<HHH", 0x206E, m_set_dom, 0x0021),
        # new-instance v2, Landroid/webkit/WebChromeClient;
        struct.pack("<HH", 0x0222, t_wcc),
        # invoke-direct {v2}, Landroid/webkit/WebChromeClient;-><init>()V
        struct.pack("<HHH", 0x1070, m_wcc_init, 0x0002),
        # invoke-virtual {v0, v2}, Landroid/webkit/WebView;->setWebChromeClient(Landroid/webkit/WebChromeClient;)V
        struct.pack("<HHH", 0x206E, m_set_wcc, 0x0020),
        # new-instance v2, Landroid/webkit/WebViewClient;
        struct.pack("<HH", 0x0222, t_wvc),
        # invoke-direct {v2}, Landroid/webkit/WebViewClient;-><init>()V
        struct.pack("<HHH", 0x1070, m_wvc_init, 0x0002),
        # invoke-virtual {v0, v2}, Landroid/webkit/WebView;->setWebViewClient(Landroid/webkit/WebViewClient;)V
        struct.pack("<HHH", 0x206E, m_set_wvc, 0x0020),
        # invoke-virtual {v3, v0}, Landroid/app/Activity;->setContentView(Landroid/view/View;)V
        struct.pack("<HHH", 0x206E, m_set_content, 0x0003),
        # const-string v2, "file:///android_asset/index.html"
        struct.pack("<HH", 0x021A, s_url),
        # invoke-virtual {v0, v2}, Landroid/webkit/WebView;->loadUrl(Ljava/lang/String;)V
        struct.pack("<HHH", 0x206E, m_load_url, 0x0020),
        # return-void
        struct.pack("<H", 0x000E),
    ])
    code_oncreate = struct.pack("<HHHHII", 5, 2, 2, 0, 0, len(insns_oncreate) // 2) + insns_oncreate

    # Layout calculation:
    # 0x00..0x70: Header (112 bytes)
    # string_ids: len(strings) * 4
    # type_ids: len(types) * 4
    # proto_ids: len(protos) * 12
    # method_ids: len(methods) * 8
    # class_defs: 1 * 32
    # Data section:
    #   type_lists (for protos with params)
    #   code_items (2 items, 4-byte aligned)
    #   string_data_items
    #   class_data_item
    #   map_list (4-byte aligned)

    header_size = 0x70
    string_ids_off = header_size
    type_ids_off = string_ids_off + len(strings) * 4
    proto_ids_off = type_ids_off + len(types) * 4
    method_ids_off = proto_ids_off + len(protos) * 12
    class_defs_off = method_ids_off + len(methods) * 8
    data_start_off = class_defs_off + 32

    data = bytearray()

    # 1. type_lists in data section
    param_tuples = []
    for p in protos:
        if len(p[2]) > 0 and p[2] not in param_tuples:
            param_tuples.append(p[2])

    type_list_offs = {}
    for pt in param_tuples:
        align4(data)
        type_list_offs[pt] = data_start_off + len(data)
        data.extend(struct.pack("<I", len(pt)))
        for t_name in pt:
            data.extend(struct.pack("<H", type_idx[t_name]))
    align4(data)

    # 2. code_items (must be 4-byte aligned)
    align4(data)
    code_init_off = data_start_off + len(data)
    data.extend(code_init)
    align4(data)
    code_oncreate_off = data_start_off + len(data)
    data.extend(code_oncreate)
    align4(data)

    # 3. string_data_items
    string_data_offs = []
    for s in strings:
        string_data_offs.append(data_start_off + len(data))
        data.extend(mutf8_string_data(s))

    # 4. class_data_item
    class_data_off = data_start_off + len(data)
    m_self_init = method_idx[("Lcom/newapi/deepseek/MainActivity;", "<init>", ("V", ()))]
    m_self_oncreate = method_idx[("Lcom/newapi/deepseek/MainActivity;", "onCreate", ("V", ("Landroid/os/Bundle;",)))]
    # static_fields=0, instance_fields=0, direct_methods=1, virtual_methods=1
    class_data = bytearray()
    class_data.extend(uleb128(0))
    class_data.extend(uleb128(0))
    class_data.extend(uleb128(1))
    class_data.extend(uleb128(1))
    # direct_method[0]: <init> (ACC_PUBLIC | ACC_CONSTRUCTOR = 0x10001)
    class_data.extend(uleb128(m_self_init))
    class_data.extend(uleb128(0x10001))
    class_data.extend(uleb128(code_init_off))
    # virtual_method[0]: onCreate (ACC_PROTECTED = 0x0004)
    class_data.extend(uleb128(m_self_oncreate))
    class_data.extend(uleb128(0x0004))
    class_data.extend(uleb128(code_oncreate_off))
    data.extend(class_data)

    # 5. map_list (4-byte aligned)
    align4(data)
    map_off = data_start_off + len(data)
    map_items = [
        (0x0000, 1, 0),                           # HEADER_ITEM
        (0x0001, len(strings), string_ids_off),   # STRING_ID_ITEM
        (0x0002, len(types), type_ids_off),       # TYPE_ID_ITEM
        (0x0003, len(protos), proto_ids_off),     # PROTO_ID_ITEM
        (0x0005, len(methods), method_ids_off),   # METHOD_ID_ITEM
        (0x0006, 1, class_defs_off),              # CLASS_DEF_ITEM
        (0x1001, len(param_tuples), data_start_off),  # TYPE_LIST
        (0x2001, 2, code_init_off),               # CODE_ITEM
        (0x2002, len(strings), string_data_offs[0]),  # STRING_DATA_ITEM
        (0x2000, 1, class_data_off),              # CLASS_DATA_ITEM
        (0x1000, 1, map_off),                     # MAP_LIST
    ]
    data.extend(struct.pack("<I", len(map_items)))
    for item_type, item_size, item_off in map_items:
        data.extend(struct.pack("<HHII", item_type, 0, item_size, item_off))

    file_size = data_start_off + len(data)
    data_size = len(data)

    # Build index tables
    string_ids_bytes = b"".join(struct.pack("<I", off) for off in string_data_offs)
    type_ids_bytes = b"".join(struct.pack("<I", str_idx[t]) for t in types)
    proto_ids_bytes = b"".join(
        struct.pack(
            "<III",
            str_idx[p[0]],
            type_idx[p[1]],
            type_list_offs.get(p[2], 0),
        )
        for p in protos
    )
    method_ids_bytes = b"".join(
        struct.pack(
            "<HHI",
            type_idx[m[0]],
            proto_idx[m[1]],
            str_idx[m[2]],
        )
        for m in methods
    )
    class_def_bytes = struct.pack(
        "<IIIIIIII",
        type_idx["Lcom/newapi/deepseek/MainActivity;"],  # class_idx
        0x0001,                                          # ACC_PUBLIC
        type_idx["Landroid/app/Activity;"],              # superclass_idx
        0,                                               # interfaces_off
        str_idx["MainActivity.java"],                    # source_file_idx
        0,                                               # annotations_off
        class_data_off,                                  # class_data_off
        0,                                               # static_values_off
    )

    header = bytearray(112)
    header[0:8] = b"dex\n035\x00"
    struct.pack_into(
        "<IIIIII",
        header,
        32,
        file_size,
        header_size,
        0x12345678,
        0,
        0,
        map_off,
    )
    struct.pack_into(
        "<IIIIIIIIIIIIII",
        header,
        56,
        len(strings), string_ids_off,
        len(types), type_ids_off,
        len(protos), proto_ids_off,
        0, 0,  # field_ids
        len(methods), method_ids_off,
        1, class_defs_off,
        data_size, data_start_off,
    )

    dex = bytearray()
    dex.extend(header)
    dex.extend(string_ids_bytes)
    dex.extend(type_ids_bytes)
    dex.extend(proto_ids_bytes)
    dex.extend(method_ids_bytes)
    dex.extend(class_def_bytes)
    dex.extend(data)

    # Compute SHA-1 over bytes [32:]
    sha1 = hashlib.sha1(dex[32:]).digest()
    dex[12:32] = sha1

    # Compute Adler32 checksum over bytes [12:]
    checksum = zlib.adler32(dex[12:]) & 0xFFFFFFFF
    struct.pack_into("<I", dex, 8, checksum)

    with open(output_path, "wb") as f:
        f.write(dex)
    print(f"Built {output_path}: {len(dex)} bytes, adler32=0x{checksum:08x}")


if __name__ == "__main__":
    build_dex(sys.argv[1])
