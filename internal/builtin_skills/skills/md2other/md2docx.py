"""
Markdown → Word (.docx) 转换工具 —— 中文专业报告模板版

适用于:
    - 技术方案 / 项目交付文档
    - 运维手册 / 网络安全报告
    - 政企客户交付件 / 等保测评报告
    - DDoS分析报告 / SSO对接说明等

用法:
    python md2docx.py input.md                        # 输出为 input.docx
    python md2docx.py input.md -o output.docx         # 指定输出文件名
    python md2docx.py input.md --cover --header --footer --numbering --pdf

支持:
    - 标题 h1-h6（黑体，纯黑）
    - 粗体 / 斜体 / 粗斜体 / 删除线 / 行内代码（Bootstrap 红）
    - 链接（无下划线，蓝色）/ 图片（居中，自动编号）
    - 有序 / 无序 / 多级嵌套列表
    - 表格（智能列宽，单元格间距）
    - 代码块（单格表格包裹，灰底 + 边框）
    - 引用块（楷体 + 左侧色条 + 浅灰底）
    - 水平分隔线 / 脚注
    - 自动封面（--cover）+ 自动目录（TOC）
    - 页眉（--header）/ 页码（--footer）
    - 标题自动编号（--numbering，检测到手写编号时自动跳过）
    - PDF 输出（--pdf，需 WPS Office）
"""

import argparse
import os
import re
import sys
from dataclasses import dataclass, field
from datetime import date
from pathlib import Path
from typing import Optional
from urllib.parse import unquote

from docx import Document
from docx.enum.table import WD_TABLE_ALIGNMENT
from docx.enum.text import WD_ALIGN_PARAGRAPH, WD_LINE_SPACING
from docx.oxml import OxmlElement
from docx.oxml.ns import qn
from docx.opc.constants import RELATIONSHIP_TYPE as RT
from docx.shared import Cm, Inches, Pt, RGBColor
from docx.text.paragraph import Paragraph

# ═══════════════════════════════════════════════════════════
#  样式常量 —— 中文专业报告模板
# ═══════════════════════════════════════════════════════════

# 字体族
FONT_NAME_BODY_CN = "宋体"
FONT_NAME_TITLE_CN = "黑体"
FONT_NAME_SUBTITLE_CN = "黑体"
FONT_NAME_QUOTE_CN = "楷体"
FONT_NAME_TABLE_CN = "微软雅黑"
FONT_NAME_EN = "Times New Roman"
FONT_CODE = "Consolas"

# 字号
FONT_SIZE_BODY = Pt(12)       # 小四
FONT_SIZE_TABLE = Pt(10.5)    # 五号
FONT_SIZE_CODE = Pt(10.5)
FONT_SIZE_FOOTNOTE = Pt(9)

# 正文颜色
COLOR_BODY = RGBColor(0, 0, 0)                 # 纯黑
COLOR_HEADING_1 = "000000"
COLOR_HEADING_2 = "000000"
COLOR_HEADING_3 = "000000"
COLOR_HEADING_4 = "000000"
COLOR_LINK = "0563C1"
COLOR_CODE_LANG = "888888"

# 标题配置
HEADING_CONFIG = {
    1: {
        "size": Pt(22),
        "color": COLOR_HEADING_1,
        "space_before": Pt(30),
        "space_after": Pt(14),
    },
    2: {
        "size": Pt(18),
        "color": COLOR_HEADING_2,
        "space_before": Pt(22),
        "space_after": Pt(10),
    },
    3: {
        "size": Pt(15),
        "color": COLOR_HEADING_3,
        "space_before": Pt(14),
        "space_after": Pt(8),
    },
    4: {
        "size": Pt(13),
        "color": COLOR_HEADING_4,
        "space_before": Pt(10),
        "space_after": Pt(6),
    },
    5: {
        "size": Pt(12),
        "color": "808080",
        "space_before": Pt(6),
        "space_after": Pt(4),
    },
    6: {
        "size": Pt(10.5),
        "color": "808080",
        "space_before": Pt(6),
        "space_after": Pt(4),
    },
}

# 表格配色
TABLE_HEADER_BG = "1F4E79"
TABLE_ROW_ODD = "FFFFFF"
TABLE_ROW_EVEN = "F7FBFF"

# 引用块配色
BLOCKQUOTE_BG = "F0F4F8"
BLOCKQUOTE_BORDER = "4472C4"

# 代码块配色
CODE_BLOCK_BG = "F7F8FA"
CODE_BLOCK_BORDER = "D9D9D9"

# 图片最大宽度
MAX_IMAGE_WIDTH = Inches(6.2)

# ── 封面常量 ──
COVER_BG_COLOR = "1F4E79"
COVER_SUBTITLE = "技术方案说明书"

@dataclass
class _DocumentState:
    base_dir: Path = field(default_factory=Path.cwd)
    image_counter: int = 0
    table_counter: int = 0
    numbering_id: Optional[int] = None


def _state(part):
    """转换状态归属文档，避免批量/并发转换串号。"""
    if not hasattr(part, "_md2other_state"):
        part._md2other_state = _DocumentState()
    return part._md2other_state

# 正文行距（国标固定值 28磅）
BODY_LINE_SPACING_FIXED = Pt(28)


# ═══════════════════════════════════════════════════════════
#  XML / OPC 辅助
# ═══════════════════════════════════════════════════════════


def set_cell_shading(cell, color_hex):
    tc_pr = cell._element.get_or_add_tcPr()
    shading = OxmlElement("w:shd")
    shading.set(qn("w:fill"), color_hex)
    shading.set(qn("w:val"), "clear")
    tc_pr.append(shading)


def set_paragraph_shading(paragraph, color_hex):
    p_pr = paragraph._element.get_or_add_pPr()
    shading = OxmlElement("w:shd")
    shading.set(qn("w:fill"), color_hex)
    shading.set(qn("w:val"), "clear")
    p_pr.append(shading)


def set_paragraph_left_border(paragraph, color_hex, width_pt=3):
    """给段落加左边框（用于 blockquote）"""
    p_pr = paragraph._element.get_or_add_pPr()
    borders = OxmlElement("w:pBdr")
    left = OxmlElement("w:left")
    left.set(qn("w:val"), "single")
    left.set(qn("w:sz"), str(width_pt * 8))
    left.set(qn("w:space"), "4")
    left.set(qn("w:color"), color_hex)
    borders.append(left)
    p_pr.append(borders)


def add_horizontal_line(doc):
    """添加水平分隔线"""
    p = doc.add_paragraph()
    p_pr = p._element.get_or_add_pPr()
    borders = OxmlElement("w:pBdr")
    bottom = OxmlElement("w:bottom")
    bottom.set(qn("w:val"), "single")
    bottom.set(qn("w:sz"), "6")
    bottom.set(qn("w:space"), "1")
    bottom.set(qn("w:color"), "999999")
    borders.append(bottom)
    p_pr.append(borders)


# ═══════════════════════════════════════════════════════════
#  页脚 — 页码
# ═══════════════════════════════════════════════════════════


def _add_page_number(section):
    """给 section 的页脚添加居中页码"""
    footer = section.footer
    footer.is_linked_to_previous = False
    p = footer.paragraphs[0] if footer.paragraphs else footer.add_paragraph()
    p.alignment = WD_ALIGN_PARAGRAPH.CENTER
    p.paragraph_format.space_before = Pt(0)
    p.paragraph_format.space_after = Pt(0)

    run = p.add_run("第 ")
    _set_run_font_simple(run, size=Pt(9), color="666666", font_name=FONT_NAME_BODY_CN)

    # PAGE 域
    fld_char_begin = OxmlElement("w:fldChar")
    fld_char_begin.set(qn("w:fldCharType"), "begin")
    run1 = p.add_run()
    run1._element.append(fld_char_begin)

    instr = OxmlElement("w:instrText")
    instr.set(qn("xml:space"), "preserve")
    instr.text = " PAGE "
    run2 = p.add_run()
    run2._element.append(instr)

    fld_char_sep = OxmlElement("w:fldChar")
    fld_char_sep.set(qn("w:fldCharType"), "separate")
    run3 = p.add_run()
    run3._element.append(fld_char_sep)

    fld_char_end = OxmlElement("w:fldChar")
    fld_char_end.set(qn("w:fldCharType"), "end")
    run4 = p.add_run()
    run4._element.append(fld_char_end)

    run = p.add_run(" 页")
    _set_run_font_simple(run, size=Pt(9), color="666666", font_name=FONT_NAME_BODY_CN)


def _add_header(section, title="技术方案说明书"):
    """给 section 添加页眉（封面页不显示）"""
    header = section.header
    header.is_linked_to_previous = False
    p = header.paragraphs[0] if header.paragraphs else header.add_paragraph()
    p.alignment = WD_ALIGN_PARAGRAPH.LEFT
    p.paragraph_format.space_before = Pt(0)
    p.paragraph_format.space_after = Pt(0)
    run = p.add_run(title)
    _set_run_font_simple(run, size=Pt(9), color="999999", font_name=FONT_NAME_BODY_CN)


# ═══════════════════════════════════════════════════════════
#  字体 / Run 辅助（细粒度函数族）
# ═══════════════════════════════════════════════════════════


def _set_run_font_simple(run, size=None, color=None, font_name=None, bold=False, italic=False):
    """基础的字体设置，不触及 eastAsia"""
    if font_name:
        run.font.name = font_name
    if size:
        run.font.size = size
    if bold:
        run.bold = True
    if italic:
        run.italic = True
    if color:
        run.font.color.rgb = RGBColor.from_string(color) if isinstance(color, str) else color


def _set_run_font_cn(run, cn_font, en_font=None, bold=False, italic=False,
                     strike=False, size=None, color=None):
    """同时设置中西文字体"""
    en_font = en_font or FONT_NAME_EN
    run.font.name = en_font
    r_pr = run._element.get_or_add_rPr()
    r_fonts = r_pr.find(qn("w:rFonts"))
    if r_fonts is None:
        r_fonts = OxmlElement("w:rFonts")
        r_pr.insert(0, r_fonts)
    r_fonts.set(qn("w:eastAsia"), cn_font)
    r_fonts.set(qn("w:ascii"), en_font)
    r_fonts.set(qn("w:hAnsi"), en_font)
    if size:
        run.font.size = size
    if bold:
        run.bold = True
    if italic:
        run.italic = True
    if strike:
        run.font.strike = True
    if color:
        run.font.color.rgb = RGBColor.from_string(color) if isinstance(color, str) else color


def set_body_font(run, bold=False, italic=False, strike=False, size=None, **kwargs):
    """正文：宋体"""
    _set_run_font_cn(run, FONT_NAME_BODY_CN, bold=bold, italic=italic, strike=strike,
                     size=size or FONT_SIZE_BODY, color=COLOR_BODY)


def set_heading_font(run, level, size=None, italic=False, strike=False, **kwargs):
    """标题：黑体 + 对应颜色"""
    cfg = HEADING_CONFIG.get(level, HEADING_CONFIG[4])
    _set_run_font_cn(run, FONT_NAME_TITLE_CN, bold=True, italic=italic, strike=strike,
                     size=size or cfg["size"], color=cfg["color"])


def set_table_header_font(run, size=None, italic=False, strike=False, **kwargs):
    """表头：微软雅黑 + 粗体"""
    _set_run_font_cn(run, FONT_NAME_TABLE_CN, bold=True, italic=italic, strike=strike,
                     size=size or FONT_SIZE_TABLE, color="000000")


def set_table_cell_font(run, size=None, bold=False, italic=False, strike=False, **kwargs):
    """表体：宋体"""
    _set_run_font_cn(run, FONT_NAME_BODY_CN, bold=bold, italic=italic, strike=strike,
                     size=size or FONT_SIZE_TABLE,
                     color=COLOR_BODY)


def set_quote_font(run, size=None, bold=False, italic=False, strike=False, **kwargs):
    """引用：楷体"""
    _set_run_font_cn(run, FONT_NAME_QUOTE_CN, bold=bold, italic=italic, strike=strike,
                     size=size or FONT_SIZE_BODY, color=COLOR_BODY)


def set_code_font(run, size=None, **kwargs):
    """代码：Consolas"""
    _set_run_font_simple(run, font_name=FONT_CODE,
                         size=size or FONT_SIZE_CODE)


def set_code_inline_font(run, size=None):
    """行内代码：Consolas + Bootstrap 风格"""
    _set_run_font_simple(run, font_name=FONT_CODE,
                         size=size or FONT_SIZE_BODY, color="C7254E")


def set_link_font(run, size=None):
    """链接：无下划线，蓝色 0563C1"""
    _set_run_font_cn(run, FONT_NAME_BODY_CN, size=size or FONT_SIZE_BODY,
                     color=COLOR_LINK)
    run.underline = False


# ═══════════════════════════════════════════════════════════
#  内联标记解析
# ═══════════════════════════════════════════════════════════


# 编译一次，代码 span 和转义符优先，避免把代码内容再次当作格式。
_INLINE_PATTERN = re.compile(
    r"(?P<escape>\\[\\`*{}\[\]()#+\-.!_>~|])"
    r"|(?P<code>(?P<ticks>`+)(?!`)(?:(?!(?P=ticks)(?!`)).)+?(?P=ticks)(?!`))"
    r"|(?P<image>!\[[^\]]*\]\((?:[^()\\]|\\.|\([^()]*\))*\))"
    r"|(?P<link>\[[^\]]*\]\((?:[^()\\]|\\.|\([^()]*\))*\))"
    r"|(?P<auto><(?:https?|mailto):[^>\s]+>)"
    r"|(?P<br><br\s*/?>)"
    r"|(?P<strong_em>\*\*\*.+?\*\*\*)"
    r"|(?P<strong>\*\*.+?\*\*)"
    r"|(?P<strong2>__(?!_)(?=\S).+?(?<!\s)__(?!_))"
    r"|(?P<em>\*(?!\*).+?(?<!\*)\*)"
    r"|(?P<em2>(?<![\w])_(?!_)(?=\S).+?(?<!\s)_(?![\w]))"
    r"|(?P<strike>~~.+?~~)"
    r"|(?P<footnote>\[\^[\w-]+\])",
    re.IGNORECASE,
)


# 列对齐：分隔行里的冒号决定每一列的对齐方式。
def _column_alignments(delimiters):
    aligns = []
    for cell in delimiters:
        left, right = cell.startswith(':'), cell.endswith(':')
        if left and right:
            aligns.append(WD_ALIGN_PARAGRAPH.CENTER)
        elif right:
            aligns.append(WD_ALIGN_PARAGRAPH.RIGHT)
        elif left:
            aligns.append(WD_ALIGN_PARAGRAPH.LEFT)
        else:
            aligns.append(None)
    return aligns


def _append_hyperlink(paragraph, target, build_runs):
    """把 build_runs 产生的 run 包进一个可点击的超链接。"""
    hyperlink = OxmlElement('w:hyperlink')
    if target.startswith('#'):
        hyperlink.set(qn('w:anchor'), target[1:])
    else:
        rel_id = paragraph.part.relate_to(target, RT.HYPERLINK, is_external=True)
        hyperlink.set(qn('r:id'), rel_id)
    first_run = len(paragraph.runs)
    build_runs()
    for run in paragraph.runs[first_run:]:
        run.font.color.rgb = RGBColor.from_string(COLOR_LINK)
        run.underline = False
        hyperlink.append(run._element)
    paragraph._p.append(hyperlink)


def _link_destination(text):
    """去掉可选的 Markdown 标题和包围路径的尖括号。"""
    text = text.strip()
    if text.startswith('<') and '>' in text:
        text = text[1:text.index('>')]
    else:
        text = re.sub(r'''\s+["'].*["']$''', '', text)
    return re.sub(r"\\([\\() ])", r"\1", text)


def add_inline_runs(paragraph, text, base_size=None, base_bold=False,
                    base_italic=False, footnotes=None, context="body",
                    heading_level=4, base_strike=False, _depth=0):
    """解析内联标记，保留所在段落的字体及嵌套格式。"""
    base_size = base_size or FONT_SIZE_BODY
    footnotes = footnotes or {}
    default_font = {
        "body": set_body_font,
        "table": set_table_cell_font,
        "table_header": set_table_header_font,
        "quote": set_quote_font,
        "heading": lambda r, **kw: set_heading_font(r, heading_level, **kw),
    }.get(context, set_body_font)

    def plain(value):
        run = paragraph.add_run(value)
        default_font(run, bold=base_bold, italic=base_italic,
                     strike=base_strike, size=base_size)
        return run

    # 不让人为构造的深度嵌套耗尽 Python 调用栈。
    if _depth >= 32:
        plain(text)
        return

    pos = 0
    for match in _INLINE_PATTERN.finditer(text):
        if match.start() > pos:
            plain(text[pos:match.start()])
        token = match.group(0)
        kind = match.lastgroup
        if kind == "escape":
            plain(token[1:])
        elif kind == "image":
            alt, target = token[2:].split('](', 1)
            _try_insert_image(paragraph, _link_destination(target[:-1]), alt)
        elif kind == "br":
            plain("").add_break()
        elif kind == "auto":
            target = token[1:-1]
            label = target[len('mailto:'):] if target.lower().startswith('mailto:') else target
            _append_hyperlink(paragraph, target, lambda: plain(label))
        elif kind == "link":
            label, target = token[1:].split('](', 1)
            target = _link_destination(target[:-1])
            # 空标签（[](...)）在 Word 里会是一个不可见链接，改用目标地址当显示文本。
            _append_hyperlink(paragraph, target, lambda: add_inline_runs(
                paragraph, label or target, base_size, base_bold, base_italic,
                footnotes, context, heading_level, base_strike, _depth + 1))
        elif kind == "footnote":
            name = token[2:-1]
            if name not in footnotes:
                # 没有定义的脚注按字面文本输出，避免出现指向空处的上标编号。
                plain(token)
            else:
                run = plain(f"[{name}]")
                run.font.size = FONT_SIZE_FOOTNOTE
                run.font.superscript = True
        elif kind == "code":
            ticks = len(match.group('ticks'))
            inner = token[ticks:-ticks].replace('\n', ' ')
            if inner.startswith(' ') and inner.endswith(' ') and inner.strip():
                inner = inner[1:-1]
            run = plain(inner)
            set_code_inline_font(run, size=base_size)
        else:
            trim = {"strong_em": 3, "strong": 2, "strong2": 2,
                    "em": 1, "em2": 1, "strike": 2}[kind]
            add_inline_runs(
                paragraph, token[trim:-trim], base_size,
                base_bold or kind in ("strong", "strong2", "strong_em"),
                base_italic or kind in ("em", "em2", "strong_em"),
                footnotes, context, heading_level,
                base_strike or kind == "strike", _depth + 1,
            )
        pos = match.end()
    if pos < len(text):
        plain(text[pos:])


def _try_insert_image(paragraph, src, alt):
    """相对 Markdown 所在目录寻找本地图片，成功后才计数并生成图注。"""
    state = _state(paragraph.part)
    src_path = Path(unquote(src))
    if not src_path.is_absolute():
        src_path = state.base_dir / src_path
    run = None
    if src_path.is_file():
        try:
            run = paragraph.add_run()
            shape = run.add_picture(str(src_path))
        except Exception as exc:
            # python-docx 的损坏/不支持图片错误不都继承 ValueError。
            if run is not None:
                paragraph._p.remove(run._element)
            print(f"警告: 无法读取图片 {src}: {exc}", file=sys.stderr)
        else:
            section = paragraph.part.document.sections[-1]
            width = min(MAX_IMAGE_WIDTH, section.page_width - section.left_margin - section.right_margin)
            cell_width = getattr(paragraph._parent, 'width', None)
            if cell_width:
                width = min(width, cell_width - Cm(0.4))
            height = section.page_height - section.top_margin - section.bottom_margin - Cm(1)
            scale = min(1, width / shape.width, height / shape.height)
            shape.width = int(shape.width * scale)
            shape.height = int(shape.height * scale)
            # 固定 28pt 行高会把图片裁掉；取消正文首行缩进。
            paragraph.paragraph_format.line_spacing = 1.0
            paragraph.paragraph_format.first_line_indent = 0
            paragraph.alignment = WD_ALIGN_PARAGRAPH.CENTER
            state.image_counter += 1
            caption_xml = OxmlElement('w:p')
            previous = getattr(paragraph, '_last_image_caption', paragraph._p)
            previous.addnext(caption_xml)
            paragraph._last_image_caption = caption_xml
            caption_p = Paragraph(caption_xml, paragraph._parent)
            caption_p.alignment = WD_ALIGN_PARAGRAPH.CENTER
            cap_run = caption_p.add_run(f"图{state.image_counter} {alt}")
            _set_run_font_simple(cap_run, size=Pt(9), color="666666",
                                 font_name=FONT_NAME_BODY_CN)
            return
    run = paragraph.add_run(f"[图片: {alt or src}]")
    set_body_font(run, italic=True, size=FONT_SIZE_BODY)
    run.font.color.rgb = RGBColor(0x99, 0x99, 0x99)


# ═══════════════════════════════════════════════════════════
#  自动封面
# ═══════════════════════════════════════════════════════════


def add_cover_page(doc, title):
    """生成封面页"""
    # 用空段落占位，把标题推到页面中部
    for _ in range(6):
        p = doc.add_paragraph()
        p.paragraph_format.space_before = Pt(0)
        p.paragraph_format.space_after = Pt(0)
        run = p.add_run("")
        _set_run_font_simple(run, size=Pt(12))

    # 蓝色装饰条
    p_bar = doc.add_paragraph()
    p_bar.alignment = WD_ALIGN_PARAGRAPH.CENTER
    p_bar.paragraph_format.space_before = Pt(24)
    p_bar.paragraph_format.space_after = Pt(12)
    run_bar = p_bar.add_run("━" * 30)
    _set_run_font_simple(run_bar, size=Pt(14), color=COVER_BG_COLOR)

    # 主标题
    p_title = doc.add_paragraph()
    p_title.alignment = WD_ALIGN_PARAGRAPH.CENTER
    p_title.paragraph_format.space_after = Pt(8)
    run_title = p_title.add_run(title)
    _set_run_font_cn(run_title, FONT_NAME_TITLE_CN, bold=True,
                     size=Pt(28), color=COVER_BG_COLOR)

    # 副标题
    p_sub = doc.add_paragraph()
    p_sub.alignment = WD_ALIGN_PARAGRAPH.CENTER
    p_sub.paragraph_format.space_after = Pt(36)
    run_sub = p_sub.add_run(COVER_SUBTITLE)
    _set_run_font_cn(run_sub, FONT_NAME_SUBTITLE_CN, bold=False,
                     size=Pt(16), color="808080")

    # 蓝色装饰条
    p_bar2 = doc.add_paragraph()
    p_bar2.alignment = WD_ALIGN_PARAGRAPH.CENTER
    p_bar2.paragraph_format.space_before = Pt(12)
    p_bar2.paragraph_format.space_after = Pt(48)
    run_bar2 = p_bar2.add_run("━" * 30)
    _set_run_font_simple(run_bar2, size=Pt(14), color=COVER_BG_COLOR)

    # 日期
    p_date = doc.add_paragraph()
    p_date.alignment = WD_ALIGN_PARAGRAPH.CENTER
    today = date.today()
    date_str = f"{today.year}年{today.month}月{today.day}日"
    run_date = p_date.add_run(date_str)
    _set_run_font_simple(run_date, size=Pt(12), color="999999",
                         font_name=FONT_NAME_BODY_CN)

    _add_cover_footer(doc)

    # 封面后的分页
    doc.add_page_break()


def _add_cover_footer(doc):
    """封面底部免责/公司信息"""
    p = doc.add_paragraph()
    p.alignment = WD_ALIGN_PARAGRAPH.CENTER
    p.paragraph_format.space_before = Pt(60)
    run = p.add_run("— 本文档仅供内部使用 —")
    _set_run_font_simple(run, size=Pt(9), color="BBBBBB", font_name=FONT_NAME_BODY_CN)


# ═══════════════════════════════════════════════════════════
#  自动目录
# ═══════════════════════════════════════════════════════════


def add_toc(doc):
    """插入 Word TOC 域 (目录)"""
    p_heading = doc.add_paragraph()
    run = p_heading.add_run("目  录")
    _set_run_font_cn(run, FONT_NAME_TITLE_CN, bold=True,
                     size=Pt(16), color=COLOR_HEADING_1)
    p_heading.alignment = WD_ALIGN_PARAGRAPH.CENTER
    p_heading.paragraph_format.space_after = Pt(12)

    # TOC 域
    p = doc.add_paragraph()
    p.paragraph_format.space_before = Pt(6)
    p.paragraph_format.space_after = Pt(6)

    # 使用 XML 插入 TOC 域
    fld_char_begin = OxmlElement("w:fldChar")
    fld_char_begin.set(qn("w:fldCharType"), "begin")
    run0 = p.add_run()
    run0._element.append(fld_char_begin)

    instr = OxmlElement("w:instrText")
    instr.set(qn("xml:space"), "preserve")
    instr.text = r'TOC \o "1-3" \h \z \u'
    run1 = p.add_run()
    run1._element.append(instr)

    fld_char_sep = OxmlElement("w:fldChar")
    fld_char_sep.set(qn("w:fldCharType"), "separate")
    run2 = p.add_run()
    run2._element.append(fld_char_sep)

    # 占位文本
    run3 = p.add_run("（请在 Word 中右键点击此处 → 更新域 生成目录）")
    _set_run_font_simple(run3, size=Pt(10), color="999999",
                         font_name=FONT_NAME_BODY_CN)

    fld_char_end = OxmlElement("w:fldChar")
    fld_char_end.set(qn("w:fldCharType"), "end")
    run4 = p.add_run()
    run4._element.append(fld_char_end)

    doc.add_page_break()


# ═══════════════════════════════════════════════════════════
#  元素构建
# ═══════════════════════════════════════════════════════════

_STRONG_NUMBERING = re.compile(
    r'^(?:\d+(?:\.\d+)+(?:[.、．)]|\s|$)'
    r'|\d+[、．)]|\d+\.(?!\d)'
    r'|[一二三四五六七八九十百千]+[、．]'
    r'|[（(][一二三四五六七八九十百千\d]+[）)]'
    r'|第[一二三四五六七八九十百千\d]+[章节篇]'
    r'|[A-Z]\)|(?:Part|Chapter)\s+\d+\b)', re.IGNORECASE
)
# “12 个要点”这类正文数字也可能像编号，单处不算数，多处出现才认为全文已手工编号。
_WEAK_NUMBERING = re.compile(r'^\d{1,3}\s+\S')


def _detect_existing_numbering(blocks):
    """判断原文是否已手工编号：带标点的编号强匹配，纯数字加空格需出现两次以上。"""
    headings = [block[2] for block in blocks if block[0] == 'heading']
    if any(_STRONG_NUMBERING.match(title) for title in headings):
        return True
    return sum(1 for title in headings if _WEAK_NUMBERING.match(title)) >= 2


def _setup_heading_numbering(doc):
    """创建标题多级编号定义（1 / 1.1 / 1.1.1 ...）"""
    numbering = doc.part.numbering_part.element
    abstract_id = max((int(el.get(qn('w:abstractNumId')))
                       for el in numbering.findall(qn('w:abstractNum'))), default=-1) + 1
    num_id = max((int(el.get(qn('w:numId')))
                  for el in numbering.findall(qn('w:num'))), default=0) + 1

    # Abstract numbering
    abs_num = OxmlElement('w:abstractNum')
    abs_num.set(qn('w:abstractNumId'), str(abstract_id))

    # 对应 Markdown 的六级标题。
    for level in range(6):
        lvl = OxmlElement('w:lvl')
        lvl.set(qn('w:ilvl'), str(level))

        start = OxmlElement('w:start')
        start.set(qn('w:val'), '1')
        lvl.append(start)

        numFmt = OxmlElement('w:numFmt')
        numFmt.set(qn('w:val'), 'decimal')
        lvl.append(numFmt)

        pStyle = OxmlElement('w:pStyle')
        pStyle.set(qn('w:val'), doc.styles[f'Heading {level + 1}'].style_id)
        lvl.append(pStyle)

        lvlText = OxmlElement('w:lvlText')
        lvlText.set(qn('w:val'), '%1、' if level == 0 else '.'.join(f'%{i + 1}' for i in range(level + 1)))
        lvl.append(lvlText)

        lvlJc = OxmlElement('w:lvlJc')
        lvlJc.set(qn('w:val'), 'left')
        lvl.append(lvlJc)

        # 编号字体 + 颜色（与对应标题一致）
        rPr = OxmlElement('w:rPr')
        cfg = HEADING_CONFIG.get(level + 1, HEADING_CONFIG[4])
        rFonts = OxmlElement('w:rFonts')
        rFonts.set(qn('w:eastAsia'), FONT_NAME_TITLE_CN)
        rFonts.set(qn('w:ascii'), FONT_NAME_EN)
        rFonts.set(qn('w:hAnsi'), FONT_NAME_EN)
        rPr.append(rFonts)

        color = OxmlElement('w:color')
        color.set(qn('w:val'), cfg['color'])
        rPr.append(color)

        sz = OxmlElement('w:sz')
        sz.set(qn('w:val'), str(int(cfg['size'].pt * 2)))
        rPr.append(sz)

        szCs = OxmlElement('w:szCs')
        szCs.set(qn('w:val'), str(int(cfg['size'].pt * 2)))
        rPr.append(szCs)

        lvl.append(rPr)
        abs_num.append(lvl)

    # OOXML 要求 abstractNum 排在所有 num 实例之前。
    first_num = numbering.find(qn('w:num'))
    if first_num is None:
        numbering.append(abs_num)
    else:
        first_num.addprevious(abs_num)

    # Numbering instance
    num = OxmlElement('w:num')
    num.set(qn('w:numId'), str(num_id))
    abstractNumId = OxmlElement('w:abstractNumId')
    abstractNumId.set(qn('w:val'), str(abstract_id))
    num.append(abstractNumId)
    numbering.append(num)
    _state(doc.part).numbering_id = num_id


def _add_numpr_to_heading(paragraph, level):
    """给标题段落添加编号引用；numPr 必须排在 pPr 的 spacing/jc 之前。"""
    pPr = paragraph._element.get_or_add_pPr()
    numPr = pPr.get_or_add_numPr()
    numPr.get_or_add_ilvl().val = level - 1
    numPr.get_or_add_numId().val = _state(paragraph.part).numbering_id


def add_heading(doc, text, level, footnotes=None):
    cfg = HEADING_CONFIG.get(level, HEADING_CONFIG[4])
    h = doc.add_heading(level=level)
    # 清除默认 run
    for r in h.runs:
        r.text = ""
    h.paragraph_format.space_before = cfg["space_before"]
    h.paragraph_format.space_after = cfg["space_after"]
    # 一级标题居中
    if level == 1:
        h.alignment = WD_ALIGN_PARAGRAPH.CENTER
    # 仅当未检测到手动编号时才添加自动编号
    if _state(doc.part).numbering_id is not None:
        _add_numpr_to_heading(h, level)
    add_inline_runs(h, text, base_size=cfg["size"], base_bold=True,
                    footnotes=footnotes, context="heading", heading_level=level)


def add_body_paragraph(doc, text, footnotes=None):
    p = doc.add_paragraph()
    p.paragraph_format.line_spacing_rule = WD_LINE_SPACING.EXACTLY
    p.paragraph_format.line_spacing = BODY_LINE_SPACING_FIXED
    p.paragraph_format.first_line_indent = Cm(0.74)  # 首行缩进2字符
    p.paragraph_format.space_after = Pt(6)
    p.alignment = WD_ALIGN_PARAGRAPH.JUSTIFY  # 两端对齐
    add_inline_runs(p, text, footnotes=footnotes, context="body")
    return p


def _content_width(text):
    """估算文本宽度：中文算2个单位，英文/数字算1个"""
    w = 0
    for ch in text:
        w += 2 if '\u4e00' <= ch <= '\u9fff' or '\u3000' <= ch <= '\u303f' else 1
    return w


def _set_column_widths(table, headers, rows, available):
    """按内容比例分配列宽"""
    col_count = len(headers)
    # 计算每列最大宽度
    col_widths = [_content_width(h) for h in headers]
    for row in rows:
        for i in range(min(len(row), col_count)):
            col_widths[i] = max(col_widths[i], _content_width(row[i]))
    # 加一点余量
    col_widths = [max(w + 2, 6) for w in col_widths]
    total = sum(col_widths)
    widths = [int(available * w / total) for w in col_widths]
    # 同时更新 tblGrid 和 tcW，避免阅读器仍按等宽网格布局。
    for column, width in zip(table.columns, widths):
        column.width = width
    for row in table.rows:
        for cell, width in zip(row.cells, widths):
            cell.width = width


def add_table(doc, headers, rows, aligns=None, footnotes=None):
    col_count = len(headers)
    if not col_count:
        raise ValueError("表格至少需要一列")
    aligns = aligns or [None] * col_count
    table = doc.add_table(rows=1 + len(rows), cols=col_count)
    table.alignment = WD_TABLE_ALIGNMENT.CENTER
    table.style = "Table Grid"
    table.autofit = False

    # 智能列宽：按内容长度分配
    section = doc.sections[-1]
    _set_column_widths(table, headers, rows,
                       section.page_width - section.left_margin - section.right_margin)

    # 表头：没写对齐时统一居中
    for c_idx, (cell, h) in enumerate(zip(table.rows[0].cells, headers)):
        cell.paragraphs[0].clear()
        add_inline_runs(cell.paragraphs[0], h, base_size=FONT_SIZE_TABLE,
                        base_bold=True, footnotes=footnotes, context="table_header")
        cell.paragraphs[0].alignment = (aligns[c_idx] if c_idx < len(aligns)
                                       and aligns[c_idx] is not None
                                       else WD_ALIGN_PARAGRAPH.CENTER)
        cell.paragraphs[0].paragraph_format.space_before = Pt(2)
        cell.paragraphs[0].paragraph_format.space_after = Pt(2)

    # 数据行
    for table_row, row in zip(list(table.rows)[1:], rows):
        for c_idx, cell in enumerate(table_row.cells):
            val = row[c_idx] if c_idx < len(row) else ""
            cell.paragraphs[0].clear()
            add_inline_runs(cell.paragraphs[0], val, base_size=FONT_SIZE_TABLE,
                            footnotes=footnotes, context="table")
            cell.paragraphs[0].alignment = aligns[c_idx] if c_idx < len(aligns) else None
            cell.paragraphs[0].paragraph_format.space_before = Pt(2)
            cell.paragraphs[0].paragraph_format.space_after = Pt(2)

    # 表号 + 表注（取第一列标题作为表名）
    state = _state(doc.part)
    state.table_counter += 1
    cap_p = doc.add_paragraph()
    cap_p.alignment = WD_ALIGN_PARAGRAPH.CENTER
    cap_p.paragraph_format.space_before = Pt(4)
    cap_p.paragraph_format.space_after = Pt(8)
    caption_text = f"表{state.table_counter} {table.cell(0, 0).text}"
    run = cap_p.add_run(caption_text)
    _set_run_font_simple(run, size=Pt(9), color="666666", font_name=FONT_NAME_BODY_CN)


def add_code_block(doc, code_text, language=""):
    """添加代码块：单格表格包裹，整体灰底 + 圆角感边框"""
    # 一行搞定：用单格表格把所有代码包在一起
    table = doc.add_table(rows=1, cols=1)
    table.alignment = WD_TABLE_ALIGNMENT.CENTER
    # 移除表格默认样式
    table.style = "Table Grid"
    # 移除所有内边距边距
    cell = table.rows[0].cells[0]
    # 清除默认段落
    cell.paragraphs[0].clear()

    # 语言标签（如果代码块有标注语言）
    if language:
        p_lang = cell.paragraphs[0]
        run = p_lang.add_run(f"  {language}")
        _set_run_font_simple(run, size=Pt(8), color=COLOR_CODE_LANG,
                             font_name=FONT_CODE)
        p_lang.paragraph_format.space_after = Pt(2)
        p_lang.paragraph_format.space_before = Pt(2)

    # 代码内容（单一段落，用换行符分隔）
    # 将代码塞入一个段落，保持行结构
    p_code = cell.add_paragraph() if language else cell.paragraphs[0]
    p_code.paragraph_format.line_spacing = 1.2
    p_code.paragraph_format.space_before = Pt(4)
    p_code.paragraph_format.space_after = Pt(4)

    # python-docx 原生保留换行和空格，无需向代码插入零宽字符。
    run = p_code.add_run(code_text)
    set_code_font(run)

    # 设置单元格底色
    set_cell_shading(cell, CODE_BLOCK_BG)
    # 设置单元格四周边框
    _set_code_cell_border(cell)

    # 缩小单元格边距
    tc_pr = cell._element.get_or_add_tcPr()
    tc_mar = OxmlElement("w:tcMar")
    for side, val in [("top", "40"), ("left", "80"), ("bottom", "40"), ("right", "80")]:
        m = OxmlElement(f"w:{side}")
        m.set(qn("w:w"), val)
        m.set(qn("w:type"), "dxa")
        tc_mar.append(m)
    tc_pr.append(tc_mar)

    doc.add_paragraph()  # 代码块后空行


def _set_code_cell_border(cell):
    """给代码块单元格设置四周边框"""
    tc_pr = cell._element.get_or_add_tcPr()
    tc_borders = OxmlElement("w:tcBorders")
    for side in ("top", "left", "bottom", "right"):
        el = OxmlElement(f"w:{side}")
        el.set(qn("w:val"), "single")
        el.set(qn("w:sz"), "4")
        el.set(qn("w:space"), "0")
        el.set(qn("w:color"), CODE_BLOCK_BORDER)
        tc_borders.append(el)
    tc_pr.append(tc_borders)


def add_blockquote(doc, blocks, footnotes=None):
    """引用块：内部按块渲染，列表/代码/表格保持各自形态。"""
    for block in blocks:
        btype = block[0]
        if btype == "text":
            _add_quote_paragraph(doc, block[1], footnotes)
        elif btype == "heading":
            _add_quote_paragraph(doc, block[2], footnotes, bold=True)
        elif btype == "unordered_list":
            for level, text in block[1]:
                add_list_item(doc, text, level=level, footnotes=footnotes, quote=True)
        elif btype == "ordered_list":
            for level, num, text in block[1]:
                add_list_item(doc, text, level=level, ordered=True, number=num,
                              footnotes=footnotes, quote=True)
        elif btype == "code":
            add_code_block(doc, block[1], block[2])
        elif btype == "table":
            add_table(doc, block[1], block[2], block[3] if len(block) > 3 else None,
                      footnotes)
        elif btype == "blockquote":
            add_blockquote(doc, block[1], footnotes)
        elif btype == "hr":
            add_horizontal_line(doc)


def _add_quote_paragraph(doc, text, footnotes=None, bold=False):
    """引用块里的正文段落：楷体 + 左侧色条 + 浅灰底"""
    p = doc.add_paragraph()
    p.paragraph_format.line_spacing_rule = WD_LINE_SPACING.EXACTLY
    p.paragraph_format.line_spacing = BODY_LINE_SPACING_FIXED
    p.paragraph_format.left_indent = Cm(0.3)
    p.paragraph_format.space_before = Pt(0)
    p.paragraph_format.space_after = Pt(0)
    set_paragraph_shading(p, BLOCKQUOTE_BG)
    set_paragraph_left_border(p, BLOCKQUOTE_BORDER, width_pt=2)
    add_inline_runs(p, text, base_size=FONT_SIZE_BODY, base_bold=bold,
                    footnotes=footnotes, context="quote")


def add_list_item(doc, text, level=0, ordered=False, number=1, footnotes=None,
                  quote=False):
    """添加列表项（支持嵌套）；quote=True 时叠加引用块底色和左色条。"""
    context = "quote" if quote else "body"
    if ordered:
        p = doc.add_paragraph()
        indent = Cm(1.27 * (level + 1))
        p.paragraph_format.left_indent = indent
        p.paragraph_format.first_line_indent = Cm(-0.63)
        p.paragraph_format.space_after = Pt(2)
        p.alignment = WD_ALIGN_PARAGRAPH.JUSTIFY
        run = p.add_run(f"{number}. ")
        set_body_font(run, size=FONT_SIZE_BODY)
        add_inline_runs(p, text, footnotes=footnotes, context=context)
    else:
        style_name = {0: "List Bullet", 1: "List Bullet 2"}.get(level, "List Bullet 3")
        try:
            p = doc.add_paragraph(style=style_name)
        except KeyError:
            p = doc.add_paragraph(style="List Bullet")
        if level >= 2:
            # 内置样式只到 List Bullet 3，更深的层级自己给缩进，否则会挤在同一位置。
            p.paragraph_format.left_indent = Cm(1.27 * (level + 1))
            p.paragraph_format.first_line_indent = Cm(-0.63)
        p.paragraph_format.space_after = Pt(2)
        p.alignment = WD_ALIGN_PARAGRAPH.JUSTIFY
        add_inline_runs(p, text, footnotes=footnotes, context=context)
    if quote:
        set_paragraph_shading(p, BLOCKQUOTE_BG)
        set_paragraph_left_border(p, BLOCKQUOTE_BORDER, width_pt=2)


# ═══════════════════════════════════════════════════════════
#  Markdown 解析器
# ═══════════════════════════════════════════════════════════


def _list_indent(line):
    """返回列表项的行首列数（Tab 按 4 列展开）。"""
    expanded = line.rstrip().expandtabs(4)
    return len(expanded) - len(expanded.lstrip())


def _assign_list_levels(indents):
    """按父项缩进推导层级，3 空格或 4 空格两种缩进习惯都能得到正确深度。"""
    levels, stack = [], []
    for indent in indents:
        while stack and indent < stack[-1]:
            stack.pop()
        if not stack or indent > stack[-1]:
            stack.append(indent)
        levels.append(len(stack) - 1)
    return levels


_FENCE = re.compile(r"^ {0,3}(`{3,}|~{3,})(.*)$")
_HEADING = re.compile(r"^ {0,3}(#{1,6})(?:\s+(.*)|\s*)$")
_LIST = re.compile(r"^(\s*)(?:[-*+]|\d+[.)])\s+")
_RULE = re.compile(r"^(?:(?:\*\s*){3,}|(?:-\s*){3,}|(?:_\s*){3,})$")


def _split_table_row(line):
    """只拆未转义的管道符，保留空白单元格及内联转义。"""
    cells, current = [], []
    i = 0
    line = line.strip()
    while i < len(line):
        char = line[i]
        if char == '\\' and i + 1 < len(line):
            current.extend(line[i:i + 2])
            i += 2
            continue
        if char == '|':
            cells.append(''.join(current).strip())
            current = []
        else:
            current.append(char)
        i += 1
    cells.append(''.join(current).strip())
    if len(cells) > 1 and cells[0] == '':
        cells.pop(0)
    if len(cells) > 1 and cells[-1] == '':
        cells.pop()
    return cells


def _table_header(lines, i):
    """返回 (表头, 列对齐)；不满足表格条件时返回 None。"""
    if i + 1 >= len(lines) or '|' not in lines[i]:
        return None
    headers = _split_table_row(lines[i])
    delimiters = _split_table_row(lines[i + 1])
    if (len(headers) == len(delimiters)
            and all(re.fullmatch(r':?-+:?', cell) for cell in delimiters)):
        return headers, _column_alignments(delimiters)
    return None


def _strip_front_matter(lines, max_lines=40):
    """剥离文档开头的 YAML front matter；不确定时宁可不剥，避免误删正文。"""
    if len(lines) < 3 or lines[0].strip() != '---':
        return lines
    if not re.match(r'^\s*[\w.-]+\s*:', lines[1]):
        return lines
    for end in range(2, min(len(lines), max_lines)):
        if lines[end].strip() in ('---', '...'):
            return lines[end + 1:]
    return lines


def _starts_block(lines, i):
    line = lines[i]
    stripped = line.strip()
    return bool(_FENCE.match(line) or _HEADING.match(line)
                or stripped.startswith(('>', '<!--'))
                or _RULE.fullmatch(stripped) or _LIST.match(line)
                or re.match(r'^\[\^[\w-]+\]:', stripped)
                or _table_header(lines, i) is not None
                or (i + 1 < len(lines) and stripped
                    and re.fullmatch(r'(?:={3,}|-{3,})', lines[i + 1].strip())))


def _end_of_line_break(raw):
    """行尾两个空格或反斜杠是 Markdown 硬换行，返回 (文本, 是否硬换行)。"""
    if raw.endswith("  "):
        return raw.strip(), True
    if raw.rstrip().endswith("\\"):
        return raw.rstrip()[:-1].strip(), True
    return raw.strip(), False


def parse_md(md_text):
    blocks = []
    footnotes = {}
    lines = _strip_front_matter(md_text.lstrip("\ufeff").splitlines())
    i = 0

    while i < len(lines):
        line = lines[i]
        stripped = line.strip()

        # 块注释后的可见内容仍需处理。
        if stripped.startswith("<!--"):
            while i < len(lines) and "-->" not in lines[i]:
                i += 1
            if i < len(lines):
                lines[i] = lines[i].split("-->", 1)[1]
                if lines[i].strip():
                    continue
            i += 1
            continue

        # 脚注定义
        fn_m = re.match(r"^\[\^([\w-]+)\]:\s*(.*)", stripped)
        if fn_m:
            fn_id, fn_text = fn_m.group(1), fn_m.group(2)
            i += 1
            while i < len(lines) and lines[i].startswith("  "):
                fn_text += " " + lines[i].strip()
                i += 1
            footnotes[fn_id] = fn_text
            continue

        # 闭合围栏必须与开围栏同字符、至少同长度，且之后只能有空白。
        fence = _FENCE.match(line)
        if fence:
            marker, info = fence.groups()
            if marker[0] != '`' or '`' not in info:
                language = info.strip()
                closing = re.compile(r'^ {0,3}' + re.escape(marker[0])
                                     + '{' + str(len(marker)) + r',}\s*$')
                code_lines = []
                i += 1
                while i < len(lines) and not closing.fullmatch(lines[i]):
                    code_lines.append(lines[i])
                    i += 1
                blocks.append(("code", "\n".join(code_lines), language))
                if i < len(lines):
                    i += 1
                continue

        h_m = _HEADING.match(line)
        if h_m:
            level = len(h_m.group(1))
            title = re.sub(r'\s+#+\s*$', '', h_m.group(2) or '').strip()
            blocks.append(("heading", level, title))
            i += 1
            continue

        # Setext 标题
        if i + 1 < len(lines) and stripped and re.match(r"^[=]{3,}$", lines[i + 1].strip()):
            blocks.append(("heading", 1, stripped))
            i += 2
            continue
        if i + 1 < len(lines) and stripped and re.match(r"^[-]{3,}$", lines[i + 1].strip()) and not re.match(r"^[-*]\s", stripped):
            blocks.append(("heading", 2, stripped))
            i += 2
            continue

        # 表格允许省略首尾管道符；严格校验分隔行，避免误吞正文。
        table_head = _table_header(lines, i)
        if table_head is not None:
            headers, aligns = table_head
            i += 2
            rows = []
            while (i < len(lines) and lines[i].strip() and "|" in lines[i]
                   and not _starts_block(lines, i)):
                rows.append(_split_table_row(lines[i]))
                i += 1
            blocks.append(("table", headers, rows, aligns))
            continue

        # 水平线
        if _RULE.fullmatch(stripped):
            blocks.append(("hr",))
            i += 1
            continue

        # 引用块整体重新解析，里面的列表、代码、表格按原样保留。
        if stripped.startswith(">"):
            quote_lines = []
            while i < len(lines) and lines[i].strip().startswith(">"):
                quote_lines.append(re.sub(r'^> ?', '', lines[i].strip()))
                i += 1
            blocks.append(("blockquote", parse_md("\n".join(quote_lines))[0]))
            continue

        # 有序列表
        ol_m = re.match(r"^(\s*)(\d+)[.)]\s+(.*)", line)
        if ol_m:
            raw_items = []
            while i < len(lines):
                ol_line = re.match(r"^(\s*)(\d+)[.)]\s+(.*)", lines[i])
                if ol_line:
                    raw_items.append((_list_indent(lines[i]), int(ol_line.group(2)),
                                      ol_line.group(3).strip()))
                    i += 1
                elif (lines[i].startswith(("  ", "	")) and raw_items
                      and not _starts_block(lines, i)):
                    prev = raw_items[-1]
                    raw_items[-1] = (prev[0], prev[1], prev[2] + " " + lines[i].strip())
                    i += 1
                else:
                    break
            levels = _assign_list_levels([indent for indent, _, _ in raw_items])
            blocks.append(("ordered_list", [(lvl, num, text) for lvl, (_, num, text)
                                            in zip(levels, raw_items)]))
            continue

        # 无序列表
        ul_m = re.match(r"^(\s*)[-*+]\s+(.*)", line)
        if ul_m:
            raw_items = []
            while i < len(lines):
                ul_line = re.match(r"^(\s*)[-*+]\s+(.*)", lines[i])
                if ul_line:
                    raw_items.append((_list_indent(lines[i]), ul_line.group(2).strip()))
                    i += 1
                elif (lines[i].startswith(("  ", "	")) and raw_items
                      and not _starts_block(lines, i)):
                    prev = raw_items[-1]
                    raw_items[-1] = (prev[0], prev[1] + " " + lines[i].strip())
                    i += 1
                else:
                    break
            levels = _assign_list_levels([indent for indent, _ in raw_items])
            blocks.append(("unordered_list", [(lvl, text) for lvl, (_, text)
                                              in zip(levels, raw_items)]))
            continue

        # 缩进代码块（4 空格或 Tab）；前面的段落或列表会先吃掉续行，能走到这里就是块首。
        if line.startswith(("    ", "\t")):
            code_lines = []
            while i < len(lines):
                current = lines[i]
                if current.startswith("\t"):
                    code_lines.append(current[1:])
                elif current.startswith("    "):
                    code_lines.append(current[4:])
                elif not current.strip():
                    code_lines.append("")
                else:
                    break
                i += 1
            while code_lines and not code_lines[-1].strip():
                code_lines.pop()
            blocks.append(("code", "\n".join(code_lines), ""))
            continue

        # 普通文本；行尾两个空格或反斜杠转为硬换行。
        if stripped:
            text, hard = _end_of_line_break(line)
            para = text
            i += 1
            while (i < len(lines) and lines[i].strip()
                   and not _starts_block(lines, i)):
                text, next_hard = _end_of_line_break(lines[i])
                para += ("<br>" if hard else " ") + text
                hard = next_hard
                i += 1
            blocks.append(("text", para))
            continue

        i += 1

    return blocks, footnotes


# ═══════════════════════════════════════════════════════════
#  文档构建
# ═══════════════════════════════════════════════════════════


def build_docx(blocks, footnotes, with_cover=False, with_numbering=False,
               with_header=False, with_footer=False, base_dir=None):
    doc = Document()
    if base_dir is not None:
        _state(doc.part).base_dir = Path(base_dir).resolve()

    # 页面设置 A4
    section = doc.sections[0]
    section.page_width = Cm(21)
    section.page_height = Cm(29.7)
    section.top_margin = Cm(2.54)
    section.bottom_margin = Cm(2.54)
    section.left_margin = Cm(3.17)
    section.right_margin = Cm(3.17)

    # 获取文档标题（第一个 h1）
    doc_title = None
    for b in blocks:
        if b[0] == "heading" and b[1] == 1:
            doc_title = b[2]
            break

    # ── 自动封面（可选，--cover 参数开启）──
    if with_cover and doc_title:
        # 封面页使用独立的第一页页眉
        section.different_first_page_header_footer = True
        add_cover_page(doc, doc_title)
        add_toc(doc)

    # ── 页眉（可选，--header 参数开启）──
    if with_header:
        _add_header(section, title=doc_title or COVER_SUBTITLE)

    # ── 页脚页码（可选，--footer 参数开启）──
    if with_footer:
        _add_page_number(section)

    # ── 标题自动编号（仅当 --numbering 开启，且原文无手动编号时）──
    if with_numbering and not _detect_existing_numbering(blocks):
        _setup_heading_numbering(doc)

    # ── 正文 ──
    for block in blocks:
        btype = block[0]

        if btype == "heading":
            _, level, text = block
            add_heading(doc, text, level, footnotes)

        elif btype == "text":
            add_body_paragraph(doc, block[1], footnotes)

        elif btype == "unordered_list":
            for level, text in block[1]:
                add_list_item(doc, text, level=level, ordered=False, footnotes=footnotes)

        elif btype == "ordered_list":
            for level, num, text in block[1]:
                add_list_item(doc, text, level=level, ordered=True, number=num, footnotes=footnotes)

        elif btype == "table":
            headers, rows = block[1], block[2]
            add_table(doc, headers, rows, block[3] if len(block) > 3 else None, footnotes)

        elif btype == "code":
            _, code_text, language = block
            add_code_block(doc, code_text, language)

        elif btype == "blockquote":
            add_blockquote(doc, block[1], footnotes)

        elif btype == "hr":
            add_horizontal_line(doc)

    # ── 脚注附录 ──
    if footnotes:
        doc.add_paragraph()
        add_horizontal_line(doc)
        h = doc.add_heading(level=3)
        p_pr = h._element.get_or_add_pPr()
        p_spacing = p_pr.find(qn("w:spacing"))
        if p_spacing is None:
            p_spacing = OxmlElement("w:spacing")
            p_pr.append(p_spacing)
        run = h.add_run("注释")
        _set_run_font_cn(run, FONT_NAME_TITLE_CN, bold=True,
                         size=Pt(14), color=COLOR_HEADING_3)
        h.paragraph_format.space_before = Pt(12)
        h.paragraph_format.space_after = Pt(6)
        for fn_id, fn_text in footnotes.items():
            p = doc.add_paragraph()
            p.paragraph_format.space_after = Pt(2)
            p.paragraph_format.first_line_indent = Cm(0.74)
            p.alignment = WD_ALIGN_PARAGRAPH.JUSTIFY
            run = p.add_run(f"[{fn_id}] ")
            _set_run_font_simple(run, bold=True, size=FONT_SIZE_FOOTNOTE,
                                 color="666666")
            add_inline_runs(p, fn_text, base_size=FONT_SIZE_FOOTNOTE, context="body")

    return doc


# ═══════════════════════════════════════════════════════════
#  CLI 入口
# ═══════════════════════════════════════════════════════════


def main():
    parser = argparse.ArgumentParser(
        description="Markdown → Word (.docx) 转换工具 —— 中文专业报告模板",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("input", help="输入 Markdown 文件路径")
    parser.add_argument("-o", "--output", help="输出 Word 文件路径（默认与输入同名 .docx）")
    parser.add_argument(
        "--cover", action="store_true",
        help="生成自动封面 + 目录（默认不生成）"
    )
    parser.add_argument(
        "--numbering", action="store_true",
        help="标题自动编号（检测到原文已有编号时自动跳过）"
    )
    parser.add_argument(
        "--header", action="store_true",
        help="添加页眉（文档标题）"
    )
    parser.add_argument(
        "--footer", action="store_true",
        help="添加页脚（第 X 页）"
    )
    parser.add_argument(
        "--pdf", action="store_true",
        help="同时生成 PDF（需本机安装 WPS Office）"
    )
    args = parser.parse_args()

    input_path = Path(args.input)
    if not input_path.is_file():
        parser.error(f"找不到文件 {input_path}")

    output_path = Path(args.output) if args.output else input_path.with_suffix(".docx")
    if output_path.suffix.lower() != ".docx":
        parser.error("输出文件必须使用 .docx 扩展名")
    targets = [output_path]
    if args.pdf:
        targets.append(output_path.with_suffix(".pdf"))
    for target in targets:
        if (target.resolve() == input_path.resolve()
                or (target.exists() and target.samefile(input_path))):
            parser.error("输出路径不能覆盖输入文件")

    try:
        md_text = input_path.read_text(encoding="utf-8-sig")
        blocks, footnotes = parse_md(md_text)
        doc = build_docx(blocks, footnotes, with_cover=args.cover,
                         with_numbering=args.numbering,
                         with_header=args.header, with_footer=args.footer,
                         base_dir=input_path.resolve().parent)
        output_path.parent.mkdir(parents=True, exist_ok=True)
        doc.save(str(output_path))
    except (OSError, UnicodeError, ValueError) as exc:
        print(f"错误: 转换失败: {exc}", file=sys.stderr)
        return 1
    print(f"✓ 已生成: {output_path}")

    # ── 可选：转 PDF（通过 WPS COM 接口）──
    if args.pdf:
        if not _convert_to_pdf(str(output_path)):
            return 1
    return 0


def _convert_to_pdf(docx_path):
    """独立 WPS 实例导出 PDF；无论在哪一步失败都释放 COM 资源。"""
    pdf_path = str(Path(docx_path).with_suffix(".pdf"))
    app = doc = None
    success = False
    try:
        import win32com.client
        app = win32com.client.DispatchEx("Kwps.Application")
        app.Visible = False
        doc = app.Documents.Open(os.path.abspath(docx_path))
        # PDF 无法由读者更新域，导出前须展开目录及页码。
        doc.Fields.Update()
        doc.ExportAsFixedFormat(os.path.abspath(pdf_path), 17)  # 17 = wdExportFormatPDF
        success = True
    except Exception as e:
        print(f"⚠ PDF 转换失败: {e}", file=sys.stderr)
        print("  请确认已安装 WPS Office 且版本支持 COM 自动化", file=sys.stderr)
    finally:
        if doc is not None:
            try:
                doc.Close(False)
            except Exception as exc:
                print(f"警告: WPS 文档关闭失败: {exc}", file=sys.stderr)
                success = False
        if app is not None:
            try:
                app.Quit()
            except Exception as exc:
                print(f"警告: WPS 退出失败: {exc}", file=sys.stderr)
                success = False
    if success:
        print(f"✓ 已生成: {pdf_path}")
    return success


if __name__ == "__main__":
    sys.exit(main())
