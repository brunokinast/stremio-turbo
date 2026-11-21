"""Draws every Stremio Turbo icon and logo, replacing Stremio's in place.

The mark: Stremio's rounded diamond in purple-to-blue with a white fast-forward
sign, drawn from scratch. (A recoloured copy of Stremio's own icon made
Windows Defender flag the app as a fake: keep the art original.)

Usage: python scripts/make-icons.py   (needs Pillow; writes into web/ and shell/)
"""
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parent.parent
S = 1024  # master size
C = S // 2
TOP, BOTTOM = (132, 96, 255), (32, 84, 224)
GRAY_TOP, GRAY_BOTTOM = (136, 136, 136), (92, 92, 92)
DARK = (12, 11, 17, 255)
WHITE = (255, 255, 255, 255)
ICO_SIZES = [(16, 16), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)]
FONT = 'C:/Windows/Fonts/segoeuisl.ttf'


def gradient(size, top, bottom):
    w, h = size
    img = Image.new('RGBA', size)
    px = img.load()
    for y in range(h):
        t = y / max(1, h - 1)
        col = tuple(round(top[i] + (bottom[i] - top[i]) * t) for i in range(3)) + (255,)
        for x in range(w):
            px[x, y] = col
    return img


def diamond_mask(scale=0.68):
    m = Image.new('L', (S, S), 0)
    side = int(S * scale)
    o = (S - side) // 2
    ImageDraw.Draw(m).rounded_rectangle([o, o, o + side, o + side], radius=int(side * 0.16), fill=255)
    return m.rotate(45, resample=Image.BICUBIC)


def fast_forward(draw, fill=WHITE, k=1.0):
    # Two play signs, centred on their visual weight (a triangle's centroid sits a third
    # of the way from its base), not on their bounding box.
    h, w, gap = 165 * k, 230 * k, -10 * k
    x2 = w + gap
    dx = C - ((w / 3) + (x2 + w / 3)) / 2
    for x0 in (0, x2):
        draw.polygon([(x0 + dx, C - h), (x0 + dx, C + h), (x0 + w + dx, C)], fill=fill)


def mark(top=TOP, bottom=BOTTOM, scale=0.68):
    img = Image.new('RGBA', (S, S), (0, 0, 0, 0))
    img.paste(gradient((S, S), top, bottom), (0, 0), diamond_mask(scale))
    fast_forward(ImageDraw.Draw(img), k=scale / 0.68)
    return img


def monochrome():
    # White diamond with the sign cut out.
    img = Image.new('RGBA', (S, S), (0, 0, 0, 0))
    img.paste(Image.new('RGBA', (S, S), WHITE), (0, 0), diamond_mask())
    cut = Image.new('L', (S, S), 0)
    fast_forward(ImageDraw.Draw(cut), fill=255)
    img.putalpha(Image.composite(Image.new('L', (S, S), 0), img.getchannel('A'), cut))
    return img


def maskable():
    # Safe zone: the mark within the middle 60 % on Stremio's dark background.
    img = Image.new('RGBA', (S, S), DARK)
    img.alpha_composite(mark(scale=0.5))
    return img


def wordmark(size, text_color=WHITE, background=None):
    w, h = size
    img = Image.new('RGBA', size, background or (0, 0, 0, 0))
    icon = mark().resize((h, h), Image.LANCZOS)
    img.alpha_composite(icon, (0, 0))
    draw = ImageDraw.Draw(img)
    text = 'stremio turbo'
    font_size = int(h * 0.42)
    while True:
        font = ImageFont.truetype(FONT, font_size)
        box = draw.textbbox((0, 0), text, font=font)
        if box[2] - box[0] <= w - h * 1.12 or font_size < 8:
            break
        font_size -= 1
    th = box[3] - box[1]
    draw.text((h * 1.08, (h - th) / 2 - box[1]), text, font=font, fill=text_color)
    return img


def save_png(img, rel, size):
    out = ROOT / rel
    img.resize(size, Image.LANCZOS).save(out)
    print('wrote', rel)


def save_ico(img, rel, mode=None):
    # Each size is scaled from the master (the app icon as it scanned clean with Windows
    # Defender; its machine-learning detection reacts even to how an icon is encoded).
    out = ROOT / rel
    if mode:
        img = img.convert(mode)
    img.save(out, sizes=ICO_SIZES)
    print('wrote', rel)


def installer_side():
    # The installer's welcome-page side image (164x314): dark gradient, mark and name.
    w, h = 164 * 4, 314 * 4
    img = gradient((w, h), (34, 22, 90), (10, 9, 20))
    icon = mark().resize((w // 2, w // 2), Image.LANCZOS)
    img.alpha_composite(icon, (w // 4, int(h * 0.28)))
    draw = ImageDraw.Draw(img)
    font = ImageFont.truetype(FONT, int(w * 0.13))
    for i, line in enumerate(('stremio', 'turbo')):
        box = draw.textbbox((0, 0), line, font=font)
        draw.text(((w - (box[2] - box[0])) / 2, int(h * 0.58) + i * int(w * 0.17)), line, font=font, fill=WHITE)
    return img.resize((164, 314), Image.LANCZOS).convert('RGB')


def main():
    color = mark()
    # Web UI
    for rel, size in [
        ('web/assets/images/icon.png', (512, 512)),
        ('web/assets/images/icon_512x512.png', (512, 512)),
        ('web/assets/images/icon_196x196.png', (196, 196)),
        ('web/assets/images/stremio_symbol.png', (256, 256)),
    ]:
        save_png(color, rel, size)
    for rel, size in [
        ('web/assets/images/maskable_icon.png', (512, 512)),
        ('web/assets/images/maskable_icon_512x512.png', (512, 512)),
        ('web/assets/images/maskable_icon_196x196.png', (196, 196)),
    ]:
        save_png(maskable(), rel, size)
    save_png(monochrome(), 'web/assets/images/monochrome_icon_512x512.png', (512, 512))
    wordmark((670 * 2, 195 * 2)).resize((670, 195), Image.LANCZOS).save(ROOT / 'web/assets/images/logo.png')
    print('wrote web/assets/images/logo.png')
    save_ico(color, 'web/assets/favicons/favicon.ico')
    # Windows app
    save_png(color, 'shell/images/stremio.png', (256, 256))
    save_ico(color, 'shell/images/stremio.ico')
    gray = mark(GRAY_TOP, GRAY_BOTTOM)
    save_ico(gray, 'shell/images/stremio_gray.ico', 'LA')
    save_ico(gray, 'shell/images/StremioRuntime.ico', 'LA')
    installer_side().save(ROOT / 'shell/images/windows-installer.bmp')
    print('wrote shell/images/windows-installer.bmp')
    header = Image.new('RGBA', (57, 57), WHITE)
    header.alpha_composite(color.resize((53, 53), Image.LANCZOS), (2, 2))
    header.save(ROOT / 'shell/images/windows-installer-header.bmp')
    print('wrote shell/images/windows-installer-header.bmp')


if __name__ == '__main__':
    main()
