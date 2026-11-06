"""运行：python -m unittest discover -s md2other -p 'test_*.py' -v"""

import io
import os
from pathlib import Path
import struct
import subprocess
import sys
import tempfile
import types
import unittest
from unittest.mock import Mock, patch
import zlib

from docx import Document
from docx.enum.text import WD_ALIGN_PARAGRAPH, WD_LINE_SPACING
from docx.oxml.ns import qn

import md2docx as md


def make_png(path, width=20, height=10):
    def chunk(kind, data):
        return (struct.pack('!I', len(data)) + kind + data
                + struct.pack('!I', zlib.crc32(kind + data)))

    path.write_bytes(
        b'\x89PNG\r\n\x1a\n'
        + chunk(b'IHDR', struct.pack('!2I5B', width, height, 8, 2, 0, 0, 0))
        + chunk(b'IDAT', zlib.compress((b'\x00' + b'\xff' * width * 3) * height))
        + chunk(b'IEND', b'')
    )


def roundtrip(doc):
    stream = io.BytesIO()
    doc.save(stream)
    stream.seek(0)
    return Document(stream)


class ParserTests(unittest.TestCase):
    def test_long_and_tilde_fences_preserve_inner_fences(self):
        cases = [
            ('````python\n```\nkeep\n````', [('code', '```\nkeep', 'python')]),
            ('~~~txt\n```\n~~~suffix\n~~~~', [('code', '```\n~~~suffix', 'txt')]),
            ('```\nlast', [('code', 'last', '')]),
        ]
        for source, expected in cases:
            with self.subTest(source=source):
                self.assertEqual(md.parse_md(source)[0], expected)

    def test_mixed_nested_lists_keep_each_item(self):
        blocks, _ = md.parse_md('- parent\n  1. child\n    - grandchild\n  2. child2\n- end')
        doc = md.build_docx(blocks, {})
        self.assertEqual([p.text for p in doc.paragraphs],
                         ['parent', '1. child', 'grandchild', '2. child2', 'end'])

    def test_optional_pipes_and_escaped_cells(self):
        blocks, _ = md.parse_md('intro\nA | B\n:--- | ---:\nx\\|y | **value**\n| z | |')
        self.assertEqual(blocks[0], ('text', 'intro'))
        self.assertEqual(blocks[1], ('table', ['A', 'B'], [['x\\|y', '**value**'], ['z', '']],
                                     [WD_ALIGN_PARAGRAPH.LEFT, WD_ALIGN_PARAGRAPH.RIGHT]))
        doc = md.build_docx(blocks, {})
        self.assertEqual(doc.tables[0].cell(1, 0).text, 'x|y')
        self.assertTrue(doc.tables[0].cell(1, 1).paragraphs[0].runs[0].bold)

    def test_short_separator_row_is_a_table_and_missing_one_is_not(self):
        blocks, _ = md.parse_md('a|b\n-|-\n1|2')
        self.assertEqual(blocks, [('table', ['a', 'b'], [['1', '2']], [None, None])])
        for source in ('| a | b |\n| x | y |', 'a | b\n---', 'a|b\n-|'):
            with self.subTest(source=source):
                self.assertFalse(any(b[0] == 'table' for b in md.parse_md(source)[0]))

    def test_list_level_follows_parent_indent(self):
        for source in ('- a\n  - b\n    - c\n      - d',
                       '- a\n    - b\n        - c\n            - d'):
            with self.subTest(source=source):
                blocks, _ = md.parse_md(source)
                self.assertEqual([level for level, _ in blocks[0][1]], [0, 1, 2, 3])
        blocks, _ = md.parse_md('- a\n  - b\n    - c\n  - e')
        self.assertEqual([level for level, _ in blocks[0][1]], [0, 1, 2, 1])
        blocks, _ = md.parse_md('- a\n  - b\n    - c\n      - d')
        doc = md.build_docx(blocks, {})
        styles = {p.text: p.style.name for p in doc.paragraphs}
        self.assertEqual(styles['a'], 'List Bullet')
        self.assertEqual(styles['b'], 'List Bullet 2')
        self.assertEqual(styles['c'], 'List Bullet 3')
        self.assertEqual(styles['d'], 'List Bullet 3')
        # 内置样式只到第三层，更深的层级必须自己给缩进，否则会挤在同一位置。
        self.assertIsNone(doc.paragraphs[1].paragraph_format.left_indent)
        self.assertIsNotNone(doc.paragraphs[2].paragraph_format.left_indent)
        self.assertGreater(doc.paragraphs[3].paragraph_format.left_indent,
                           doc.paragraphs[2].paragraph_format.left_indent)

    def test_blockquote_keeps_inner_lists_and_nesting(self):
        blocks, _ = md.parse_md('> 引用正文\n>\n> - 甲\n> - 乙\n> 1. 丙\n>\n> > 内层引用')
        self.assertEqual(blocks[0][0], 'blockquote')
        self.assertEqual([b[0] for b in blocks[0][1]],
                         ['text', 'unordered_list', 'ordered_list', 'blockquote'])
        doc = md.build_docx(blocks, {})
        styles = {p.text: p.style.name for p in doc.paragraphs}
        self.assertEqual(styles['引用正文'], 'Normal')
        self.assertEqual(styles['甲'], 'List Bullet')
        self.assertEqual(styles['乙'], 'List Bullet')
        self.assertEqual(styles['1. 丙'], 'Normal')
        self.assertEqual(styles['内层引用'], 'Normal')
        shading = doc.paragraphs[1]._p.pPr.find(qn('w:shd'))
        self.assertIsNotNone(shading)

    def test_paragraph_does_not_swallow_comments_or_setext(self):
        blocks, footnotes = md.parse_md('\ufeff# Title\r\n\r\nbody\n<!-- hidden -->visible\n\nheading\n===\n\nref[^a-b]\n\n[^a-b]: note')
        self.assertEqual(blocks, [('heading', 1, 'Title'), ('text', 'body'),
                                  ('text', 'visible'), ('heading', 1, 'heading'),
                                  ('text', 'ref[^a-b]')])
        self.assertEqual(footnotes, {'a-b': 'note'})

    def test_atx_closing_markers_and_nonheadings(self):
        blocks, _ = md.parse_md('   ## Heading ##\n\n#hashtag\n#\n\n* * *')
        self.assertEqual(blocks, [('heading', 2, 'Heading'), ('text', '#hashtag'),
                                  ('heading', 1, ''), ('hr',)])

    def test_front_matter_is_not_body_text(self):
        blocks, _ = md.parse_md('---\ntitle: 报告\ntags: [a, b]\n---\n\n# 标题一')
        self.assertEqual(blocks, [('heading', 1, '标题一')])
        # 不像 front matter 的 --- 仍按普通分隔线/标题处理。
        blocks, _ = md.parse_md('---\n正文\n---')
        self.assertEqual(blocks, [('hr',), ('heading', 2, '正文')])
        # 开头是分隔线、后面碰巧有 key: 行时不能把中间内容当成 front matter 删掉。
        source = '---\n\n注意: 这不是 front matter\n\n' + '\n\n'.join(['正文'] * 3) + '\n\n---\n\n结尾'
        blocks, _ = md.parse_md(source)
        self.assertEqual([b[0] for b in blocks][:2], ['hr', 'text'])
        self.assertIn('结尾', [b[1] for b in blocks if b[0] == 'text'])

    def test_indented_code_block(self):
        blocks, _ = md.parse_md('    code\n    more\n\nafter')
        self.assertEqual(blocks, [('code', 'code\nmore', ''), ('text', 'after')])
        blocks, _ = md.parse_md('段落\n    续行仍是正文')
        self.assertEqual(blocks, [('text', '段落 续行仍是正文')])

    def test_table_column_alignment_is_applied(self):
        blocks, _ = md.parse_md('| a | b | c |\n|:--|:-:|--:|\n| 1 | 2 | 3 |')
        doc = md.build_docx(blocks, {})
        cells = doc.tables[0].rows[1].cells
        self.assertEqual([c.paragraphs[0].alignment for c in cells],
                         [WD_ALIGN_PARAGRAPH.LEFT, WD_ALIGN_PARAGRAPH.CENTER,
                          WD_ALIGN_PARAGRAPH.RIGHT])
        self.assertEqual(doc.tables[0].rows[0].cells[0].paragraphs[0].alignment,
                         WD_ALIGN_PARAGRAPH.LEFT)

    def test_underscore_emphasis_and_snake_case(self):
        p = Document().add_paragraph()
        md.add_inline_runs(p, '_甲_ __乙__ snake_case_name')
        self.assertEqual(p.text, '甲 乙 snake_case_name')
        runs = {run.text: run for run in p.runs}
        self.assertTrue(runs['甲'].italic)
        self.assertTrue(runs['乙'].bold)
        self.assertFalse(runs['甲'].bold)

    def test_hard_break_by_trailing_spaces_or_backslash(self):
        blocks, _ = md.parse_md('a  \nb')
        self.assertEqual(blocks, [('text', 'a<br>b')])
        blocks, _ = md.parse_md('a\\\nb')
        self.assertEqual(blocks, [('text', 'a<br>b')])
        blocks, _ = md.parse_md('a\nb')
        self.assertEqual(blocks, [('text', 'a b')])

    def test_break_tag_and_autolink(self):
        p = Document().add_paragraph()
        md.add_inline_runs(p, '上行<br>下行')
        self.assertEqual(p.text, '上行\n下行')
        self.assertTrue(p._p.xpath('.//w:br'))
        doc = roundtrip(md.build_docx([('text', '见 <https://example.com/a> 说明')], {}))
        self.assertEqual(doc.paragraphs[0].text, '见 https://example.com/a 说明')
        self.assertEqual(doc.paragraphs[0].hyperlinks[0].url, 'https://example.com/a')

    def test_footnote_marker_inside_table_cell(self):
        blocks, footnotes = md.parse_md('| 列 |\n|---|\n| 内容[^t] |\n\n[^t]: 表内脚注')
        doc = roundtrip(md.build_docx(blocks, footnotes))
        cell_run = doc.tables[0].rows[1].cells[0].paragraphs[0].runs[-1]
        self.assertEqual(cell_run.text, '[t]')
        self.assertTrue(cell_run.font.superscript)
        self.assertTrue(any(p.text == '[t] 表内脚注' for p in doc.paragraphs))

    def test_undefined_footnote_stays_literal(self):
        p = Document().add_paragraph()
        md.add_inline_runs(p, '引用[^nope]')
        self.assertEqual(p.text, '引用[^nope]')
        p = Document().add_paragraph()
        md.add_inline_runs(p, '引用[^a]', footnotes={'a': '注释'})
        self.assertEqual(p.text, '引用[a]')
        self.assertTrue(p.runs[-1].font.superscript)


class DocumentTests(unittest.TestCase):
    def test_formatting_in_body_table_quote_and_heading(self):
        for context in ('body', 'table', 'quote', 'heading'):
            with self.subTest(context=context):
                p = Document().add_paragraph()
                md.add_inline_runs(p, '**bold** *italic* ~~strike~~ ***both***', context=context)
                runs = {run.text: run for run in p.runs}
                self.assertTrue(runs['bold'].bold)
                self.assertTrue(runs['italic'].italic)
                self.assertTrue(runs['strike'].font.strike)
                self.assertTrue(runs['both'].bold)
                self.assertTrue(runs['both'].italic)

    def test_inline_code_escapes_and_nested_formatting(self):
        p = Document().add_paragraph()
        md.add_inline_runs(p, r'\*literal\* `**code**` ``a`b`` **~~nested~~**')
        self.assertEqual(p.text, '*literal* **code** a`b nested')
        run = p.runs[-1]
        self.assertTrue(run.bold)
        self.assertTrue(run.font.strike)
        self.assertIsNone(next(r for r in p.runs if r.text == '**code**').bold)

    def test_links_keep_relationship_target_and_label_formatting(self):
        doc = md.build_docx([('text', '[**site**](https://example.com/a(b) "Title")')], {})
        p = roundtrip(doc).paragraphs[0]
        self.assertEqual(p.text, 'site')
        self.assertEqual(p.hyperlinks[0].url, 'https://example.com/a(b)')
        self.assertTrue(p.hyperlinks[0].runs[0].bold)

    def test_empty_link_label_falls_back_to_target(self):
        doc = roundtrip(md.build_docx([('text', '见 [](https://example.com/x) 说明')], {}))
        p = doc.paragraphs[0]
        self.assertEqual(p.text, '见 https://example.com/x 说明')
        self.assertEqual(p.hyperlinks[0].url, 'https://example.com/x')

    def test_numbering_is_per_document_and_supports_six_levels(self):
        docs = [md.build_docx([('heading', n, f'Level {n}') for n in range(1, 7)], {},
                              with_numbering=True) for _ in range(2)]
        for doc in docs:
            doc = roundtrip(doc)
            numbering = doc.part.numbering_part.element
            for level, paragraph in enumerate(doc.paragraphs):
                self.assertEqual(paragraph.style.name, f'Heading {level + 1}')
                num_pr = paragraph._p.pPr.numPr
                self.assertEqual(num_pr.ilvl.val, level)
                num = numbering.xpath(f'w:num[@w:numId="{num_pr.numId.val}"]')[0]
                abstract_id = num.find(qn('w:abstractNumId')).get(qn('w:val'))
                abstract = numbering.xpath(f'w:abstractNum[@w:abstractNumId="{abstract_id}"]')[0]
                self.assertEqual(len(abstract.findall(qn('w:lvl'))), 6)
                self.assertEqual(abstract.findall(qn('w:lvl'))[level].find(qn('w:pStyle')).get(qn('w:val')),
                                 paragraph.style.style_id)
                self.assertEqual([el.tag for el in num_pr], [qn('w:ilvl'), qn('w:numId')])
            self.assertEqual(str(doc.paragraphs[4].runs[0].font.color.rgb), '808080')

    def test_manual_numbering_without_spaces_and_year_titles(self):
        for title in ('一、概述', '（一）概述', '1.概述', '1.1 概述', '第一章概述', '1、概述'):
            with self.subTest(title=title):
                doc = md.build_docx([('heading', 1, title)], {}, with_numbering=True)
                self.assertIsNone(doc.paragraphs[0]._p.pPr.numPr)
        self.assertFalse(md._detect_existing_numbering([('heading', 1, '2026 年度报告')]))

    def test_single_number_like_heading_keeps_auto_numbering(self):
        # “12 个要点”这种普通数字不应一次性关掉全文自动编号。
        self.assertFalse(md._detect_existing_numbering([('heading', 1, '12 个要点')]))
        self.assertTrue(md._detect_existing_numbering(
            [('heading', 1, '1 背景'), ('heading', 2, '2 方法')]))

    def test_heading_numpr_precedes_spacing_and_alignment(self):
        doc = md.build_docx([('heading', 1, 'Title')], {}, with_numbering=True)
        tags = [el.tag for el in doc.paragraphs[0]._p.pPr]
        self.assertIn(qn('w:numPr'), tags)
        self.assertLess(tags.index(qn('w:numPr')), tags.index(qn('w:spacing')))
        self.assertLess(tags.index(qn('w:numPr')), tags.index(qn('w:jc')))

    def test_image_paths_captions_scaling_and_counters(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            make_png(root / 'small image.png')
            make_png(root / 'large.png', 2000, 2000)
            (root / 'bad.png').write_bytes(b'invalid')
            blocks = [('text', '![missing](missing.png) ![bad](bad.png)'),
                      ('text', '![small](small%20image.png) ![large](large.png)')]
            for _ in range(2):
                with patch('sys.stderr', new=io.StringIO()):
                    doc = roundtrip(md.build_docx(blocks, {}, base_dir=root))
                self.assertEqual(len(doc.inline_shapes), 2)
                texts = [p.text for p in doc.paragraphs]
                self.assertIn('图1 small', texts)
                self.assertIn('图2 large', texts)
                self.assertLess(texts.index('图1 small'), texts.index('图2 large'))
                self.assertNotIn('[图片: small]', ''.join(texts))
                self.assertEqual(doc.inline_shapes[0].width, md.Inches(20 / 72))
                section = doc.sections[0]
                self.assertLessEqual(doc.inline_shapes[1].width,
                                     section.page_width - section.left_margin - section.right_margin)
                image_p = next(p for p in doc.paragraphs if p._p.xpath('.//w:drawing'))
                self.assertNotEqual(image_p.paragraph_format.line_spacing_rule, WD_LINE_SPACING.EXACTLY)

    def test_table_counters_and_grid_widths(self):
        docs = [md.build_docx([('table', ['**name**', 'details'], [['a', 'x' * 100]])], {})
                for _ in range(2)]
        for doc in docs:
            self.assertTrue(any(p.text.startswith('表1 ') for p in doc.paragraphs))
            table = doc.tables[0]
            self.assertGreater(table.columns[1].width, table.columns[0].width)
            self.assertEqual(table.rows[1].cells[1].width, table.columns[1].width)
            self.assertEqual(table.cell(0, 0).text, 'name')

    def test_code_block_preserves_blank_lines_without_hidden_characters(self):
        code = '  start\n\n\tend\n'
        doc = roundtrip(md.build_docx([('code', code, 'python')], {}))
        self.assertEqual(doc.tables[0].cell(0, 0).paragraphs[1].text, code)

    def test_cover_header_footer_and_footnotes_can_be_saved(self):
        doc = roundtrip(md.build_docx([('heading', 1, 'Title'), ('text', 'text[^a-b]')],
                                     {'a-b': '**note**'}, with_cover=True,
                                     with_numbering=True, with_header=True, with_footer=True))
        self.assertTrue(doc.sections[0].different_first_page_header_footer)
        self.assertEqual(doc.sections[0].header.paragraphs[0].text, 'Title')
        self.assertTrue(any('TOC ' in text for text in doc.element.xpath('//w:instrText/text()')))
        self.assertTrue(any(p.text == '[a-b] note' for p in doc.paragraphs))


class CliAndPdfTests(unittest.TestCase):
    def run_cli(self, *args):
        return subprocess.run([sys.executable, str(Path(md.__file__).resolve()), *map(str, args)],
                              capture_output=True, text=True, encoding='utf-8',
                              env={**os.environ, 'PYTHONIOENCODING': 'utf-8'})

    def test_missing_input_fails_and_cannot_overwrite_input(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.assertNotEqual(self.run_cli(root / 'missing.md').returncode, 0)
            source = root / 'source.docx'
            source.write_text('# Title', encoding='utf-8')
            self.assertNotEqual(self.run_cli(source, '-o', source).returncode, 0)
            self.assertEqual(source.read_text(encoding='utf-8'), '# Title')

    def test_cli_resolves_images_from_source_directory_and_creates_output_parent(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            make_png(root / 'image.png')
            source = root / 'input.md'
            source.write_text('# 标题\n\n![图片](image.png)', encoding='utf-8-sig')
            output = root / 'nested' / 'output.docx'
            result = self.run_cli(source, '-o', output, '--numbering')
            self.assertEqual(result.returncode, 0, result.stderr)
            doc = Document(output)
            self.assertEqual(doc.paragraphs[0].text, '标题')
            self.assertEqual(len(doc.inline_shapes), 1)

    def test_pdf_failure_causes_nonzero_cli_status(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / 'input.md'
            source.write_text('body', encoding='utf-8')
            with patch.object(sys, 'argv', ['md2docx', str(source), '--pdf']), \
                    patch.object(md, '_convert_to_pdf', return_value=False), \
                    patch('sys.stdout', new=io.StringIO()):
                self.assertEqual(md.main(), 1)
            self.assertTrue(source.with_suffix('.docx').is_file())

    def test_com_cleanup_on_open_export_close_and_quit_failures(self):
        for failure in ('open', 'fields', 'export', 'close', 'quit', None):
            with self.subTest(failure=failure):
                app, document = Mock(), Mock()
                app.Documents.Open.return_value = document
                method = {'open': app.Documents.Open, 'fields': document.Fields.Update,
                          'export': document.ExportAsFixedFormat,
                          'close': document.Close, 'quit': app.Quit}.get(failure)
                if method is not None:
                    method.side_effect = RuntimeError('test error')
                client = types.ModuleType('win32com.client')
                client.DispatchEx = Mock(return_value=app)
                package = types.ModuleType('win32com')
                package.client = client
                with patch.dict(sys.modules, {'win32com': package, 'win32com.client': client}), \
                        patch('sys.stderr', new=io.StringIO()), patch('sys.stdout', new=io.StringIO()):
                    success = md._convert_to_pdf('test.docx')
                self.assertEqual(success, failure is None)
                app.Quit.assert_called_once()
                if failure != 'open':
                    document.Close.assert_called_once_with(False)
                if failure in ('open', 'fields'):
                    document.ExportAsFixedFormat.assert_not_called()


if __name__ == '__main__':
    unittest.main()
