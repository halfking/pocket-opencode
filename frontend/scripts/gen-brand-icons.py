#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
AI Work Mess 品牌图标生成器 v2 —— 基于「开轩 logo」。

素材（仓库内）：frontend/brand/开轩logo-亮色.png / 开轩logo-暗色.png
  - 亮色版 = 深蓝墨迹、内部明暗层次丰富 → 用作字形细节源（亮度→色彩映射）
  - 暗色版 = 浅色墨迹 → 同几何，留作浅底/未来浅色主题备用

加色语义（「开轩见光」的价值叙事，逐像素按原始明度上色，保留 logo 造型与层次）：
  深靛蓝   #1E246E  沉潜与技术的底色（阴影）
  兰紫     #B37BEB  AI 创造力（中间调）
  破晓金    #FFC46A → #FFE096  开轩迎来的光＝价值与机遇（高光，占比最大）
底衬为品牌渐变 #667EEA→#764BA2（与 index.html theme-color 一致）。

输出（覆盖式，全部保持原文件尺寸/路径约定）：
  Android launcher/round（5 档）+ adaptive foreground（5 档）
  Android splash（11 档）· iOS AppIcon 1024 · iOS Splash 2732² ×3
  Web favicon.png 64 · apple-touch-icon.png 180

重跑：python3 frontend/scripts/gen-brand-icons.py
"""
import os

import numpy as np
from PIL import Image, ImageDraw, ImageFont

FRONTEND = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
BRAND = os.path.join(FRONTEND, "brand")
LOGO = os.path.join(BRAND, "开轩logo-亮色.png")

BG_A = np.array([0x66, 0x7E, 0xEA], dtype=float)  # 品牌靛蓝
BG_B = np.array([0x76, 0x4B, 0xA2], dtype=float)  # 品牌紫罗兰
WHITE = (255, 255, 255, 255)
WORDMARK = "AI Work Mess"
FONT_PATH = "/System/Library/Fonts/Supplemental/Arial Bold.ttf"

# 秩分位 → 色彩 停靠点（分位为「墨迹亮度在全部墨迹中的百分位」）。
# 用秩（分位）而非绝对亮度：本 logo 亮度极度集中（p25–p75 落在 20–52，
# 仅 5% 超过 179），线性拉伸会把 ~87% 墨迹压成同一暗色，"破晓金" 读不出来。
# 改用 CDF 秩映射后，下面三个色区的面积占比恒定且可调。
#   0–28%  深靛蓝  沉潜：技术沉淀（暗部保留 logo 原有体积感）
#   28–62% 兰紫    AI 创造力（过渡带）
#   62–100% 破晓金 价值与光（本 logo 的主体色，占最大面积）
RANK_STOPS = [
    (0.00, (26, 32, 96)),
    (0.28, (92, 78, 198)),
    (0.62, (255, 198, 112)),
    (1.00, (255, 232, 168)),
]


def gradient(w: int, h: int) -> Image.Image:
    t = (np.linspace(0, 1, w)[None, :] + np.linspace(0, 1, h)[:, None]) / 2.0
    arr = (1 - t[..., None]) * BG_A + t[..., None] * BG_B
    return Image.fromarray(arr.astype(np.uint8), "RGB")


def _rank_lut() -> np.ndarray:
    """256 级「墨迹亮度分位秩」→RGB 查找表（分段线性）。"""
    lut = np.zeros((256, 3), dtype=float)
    for i in range(256):
        t = i / 255.0
        for k in range(len(RANK_STOPS) - 1):
            t0, c0 = RANK_STOPS[k]
            t1, c1 = RANK_STOPS[k + 1]
            if t <= t1:
                f = 0.0 if t1 == t0 else (t - t0) / (t1 - t0)
                f = max(0.0, min(1.0, f))
                lut[i] = np.array(c0) + f * (np.array(c1) - np.array(c0))
                break
        else:
            lut[i] = RANK_STOPS[-1][1]
    return lut.clip(0, 255).astype(np.uint8)


LUT = _rank_lut()

_LOGO_CROPPED: dict = {}


def recolored_logo(out_size: int) -> Image.Image:
    """按墨迹亮度的**分位秩**把开轩 logo 上色（保留层次与体积感）。"""
    if not _LOGO_CROPPED:
        src = Image.open(LOGO).convert("RGBA")
        a = np.array(src).astype(float)
        alpha = a[..., 3]
        ink = alpha >= 128
        if not ink.any():
            raise SystemExit(f"logo 无墨迹像素: {LOGO}")
        rgb = a[..., :3]
        lum = 0.299 * rgb[..., 0] + 0.587 * rgb[..., 1] + 0.114 * rgb[..., 2]
        # 只用墨迹像素的经验 CDF 求秩，半透明羽化像素沿用最近墨迹秩（clamp）。
        ink_lum = np.sort(lum[ink])
        rank = np.searchsorted(ink_lum, lum, side="right") / float(len(ink_lum))
        rank = np.clip(rank, 0.0, 1.0)
        col = LUT[(rank * 255).astype(int)]
        # 上色后把 alpha 归一化到不透明：源 logo 的 alpha 是二值 192，
        # 直接叠加会让 25% 底色透进来，把金色洗淡（实测 43.9%→14.5%）。
        # 羽化边缘（0<alpha<255）按比例保留相对羽化。
        out_alpha = np.where(alpha > 0, np.clip(alpha / alpha.max() * 255.0, 0, 255), 0.0)
        img = Image.fromarray(np.dstack([col, out_alpha]).astype(np.uint8), "RGBA")
        ys, xs = np.where(ink)
        pad = 4
        box = (max(0, xs.min() - pad), max(0, ys.min() - pad),
               min(img.width, xs.max() + pad + 1), min(img.height, ys.max() + pad + 1))
        _LOGO_CROPPED["img"] = img.crop(box)
    im = _LOGO_CROPPED["img"].copy()
    im.thumbnail((out_size, out_size), Image.LANCZOS)
    return im


def ss_for(target: int) -> int:
    """小尺寸 4x 超采样；大尺寸受源分辨率(墨迹 bbox≈500px)限制，不做无益放大。"""
    return max(1, min(4, 768 // max(1, target)))


def paste_fit(base: Image.Image, glyph: Image.Image, size: int,
              cx: float = 0.5, cy: float = 0.5) -> None:
    """把 glyph 居中贴到 base（像素坐标按 size 归一）。"""
    gx = int(size * cx) - glyph.width // 2
    gy = int(size * cy) - glyph.height // 2
    base.paste(glyph, (gx, gy), glyph)


def rounded_mask(s: int, radius_ratio: float = 0.22) -> Image.Image:
    m = Image.new("L", (s, s), 0)
    ImageDraw.Draw(m).rounded_rectangle([0, 0, s - 1, s - 1], radius=int(s * radius_ratio), fill=255)
    return m


def circle_mask(s: int) -> Image.Image:
    m = Image.new("L", (s, s), 0)
    ImageDraw.Draw(m).ellipse([0, 0, s - 1, s - 1], fill=255)
    return m


def render_tile(target: int, shape: str = "round") -> Image.Image:
    ss = ss_for(target)
    T = target * ss
    grad = gradient(T, T).convert("RGBA")
    glyph = recolored_logo(int(T * 0.66))
    canvas = Image.new("RGBA", (T, T), (0, 0, 0, 0))
    paste_fit(canvas, glyph, T, cy=0.48)
    comp = Image.alpha_composite(grad, canvas)
    if shape != "square":
        mask = rounded_mask(T) if shape == "round" else circle_mask(T)
        bg = Image.new("RGBA", (T, T), (0, 0, 0, 0))
        bg.paste(comp, (0, 0), mask)
        comp = bg
    return comp.resize((target, target), Image.LANCZOS)


def render_foreground(target: int) -> Image.Image:
    ss = ss_for(target)
    T = target * ss
    out = Image.new("RGBA", (T, T), (0, 0, 0, 0))
    glyph = recolored_logo(int(T * 0.60))  # adaptive 安全区
    paste_fit(out, glyph, T, cy=0.48)
    return out.resize((target, target), Image.LANCZOS)


def render_splash(w: int, h: int) -> Image.Image:
    """启动图：品牌渐变底 + 上色后的开轩 logo + 白色字标（整体垂直居中）。"""
    ss = max(1, min(4, 768 // max(w, h)))
    W, H = w * ss, h * ss
    out = gradient(W, H).convert("RGBA")
    gsize = int(min(W, H) * 0.42)
    glyph = recolored_logo(gsize)
    font = ImageFont.truetype(FONT_PATH, int(min(W, H) * 0.075))
    tb = ImageDraw.Draw(out).textbbox((0, 0), WORDMARK, font=font)
    tw, th = tb[2] - tb[0], tb[3] - tb[1]
    gap = int(min(W, H) * 0.05)
    y0 = (H - (gsize + gap + th)) // 2
    out.paste(glyph, ((W - glyph.width) // 2, y0 + gsize // 2 - glyph.height // 2), glyph)
    d = ImageDraw.Draw(out)
    d.text(((W - tw) // 2 - tb[0], y0 + gsize + gap - tb[1]), WORDMARK, font=font, fill=WHITE)
    return out.resize((w, h), Image.LANCZOS)


MIPMAPS = {"mdpi": 48, "hdpi": 72, "xhdpi": 96, "xxhdpi": 144, "xxxhdpi": 192}
FG_SIZES = {"mdpi": 108, "hdpi": 162, "xhdpi": 216, "xxhdpi": 324, "xxxhdpi": 432}
SPLASHES = [
    ("drawable", 480, 320),
    ("drawable-port-mdpi", 320, 480),
    ("drawable-port-hdpi", 480, 800),
    ("drawable-port-xhdpi", 720, 1280),
    ("drawable-port-xxhdpi", 960, 1600),
    ("drawable-port-xxxhdpi", 1280, 1920),
    ("drawable-land-mdpi", 480, 320),
    ("drawable-land-hdpi", 800, 480),
    ("drawable-land-xhdpi", 1280, 720),
    ("drawable-land-xxhdpi", 1600, 960),
    ("drawable-land-xxxhdpi", 1920, 1280),
]


def save(img: Image.Image, path: str):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    img.save(path, "PNG")
    print(f"  wrote {os.path.relpath(path, FRONTEND)} {img.size}")


def main():
    if not os.path.exists(LOGO):
        raise SystemExit(f"缺少 logo 源文件: {LOGO}")
    print("[gen-brand-icons] 开轩 logo + 破晓金配色 →", FRONTEND)
    res = os.path.join(FRONTEND, "android", "app", "src", "main", "res")
    for d, size in MIPMAPS.items():
        save(render_tile(size, "round"), os.path.join(res, f"mipmap-{d}", "ic_launcher.png"))
        save(render_tile(size, "circle"), os.path.join(res, f"mipmap-{d}", "ic_launcher_round.png"))
    for d, size in FG_SIZES.items():
        save(render_foreground(size), os.path.join(res, f"mipmap-{d}", "ic_launcher_foreground.png"))
    for d, w, h in SPLASHES:
        save(render_splash(w, h), os.path.join(res, d, "splash.png"))
    ios = os.path.join(FRONTEND, "ios", "App", "App", "Assets.xcassets")
    save(render_tile(1024, "square"),
         os.path.join(ios, "AppIcon.appiconset", "AppIcon-512@2x.png"))
    for name in ("splash-2732x2732.png", "splash-2732x2732-1.png", "splash-2732x2732-2.png"):
        save(render_splash(2732, 2732), os.path.join(ios, "Splash.imageset", name))
    save(render_tile(64, "round"), os.path.join(FRONTEND, "public", "favicon.png"))
    save(render_tile(180, "square"), os.path.join(FRONTEND, "public", "apple-touch-icon.png"))
    print("[gen-brand-icons] done")


if __name__ == "__main__":
    main()
