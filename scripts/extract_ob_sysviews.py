#!/usr/bin/env python3
"""Extract OceanBase system-view reference sections from the PDF into Markdown.

Usage:
  python extract_ob_sysviews.py <pdf> <start_page> <end_page> <out.md>

`start_page`/`end_page` are 1-indexed INCLUSIVE PDF page numbers.
The section between them (one tenant's system views) is rendered as Markdown:
  * view headings  -> ## <num> <full-name>
  * sub-sections   -> ### / ####
  * column tables  -> markdown tables (auto-detects Oracle/MySQL header forms)
  * query examples -> preserved as fenced code blocks (ASCII result boxes intact)

Robustness:
  * drops running header/footer noise + per-page "next view" preview lines
  * joins view names that wrap across lines (e.g. information_schema.<name>)
  * only real section-start headers are detected (first line of a page that has
    a matching "<num>." sub-section on the same page), so content noise and
    footer previews are never mistaken for view headings.
"""

import argparse
import re
import sys

import pdfplumber

VIEW_RE = re.compile(r'^(\d+)\s+(\S+)$')
SUB_RE = re.compile(r'^(\d+(?:\.\d+)+)\s+(.+)$')
NOISE = ('OceanBase 数据库 参考指南--系统视图', '参考指南--系统视图OceanBase 数据库',
         'OceanBase 数据库 使用指南--系统参考', '使用指南--系统参考OceanBase 数据库')
RUNNING_TITLE_RE = re.compile(r'^OceanBase 数据库 .*(系统视图|系统参考)')
CODE_RE = re.compile(
    r'^(?:\+[+\-=]+|\||\*+|obclient|\d+\s*row[s]?\s*in\s*set|'
    r'(?:SELECT|INSERT|UPDATE|DELETE|CREATE|ALTER|DROP|SHOW|DESC|EXPLAIN|COMMIT|SET|MERGE|WITH)\b)',
    re.I)
GV_ROW_RE = re.compile(r'\b[A-Z][A-Z0-9_]*:\s')
CJK_RE = re.compile(r'^[\u3000-\u9fff]')


def is_noise(t):
    t = t.strip()
    if not t:
        return True
    for n in NOISE:
        if t == n or t.startswith(n):
            return True
    if RUNNING_TITLE_RE.match(t):
        return True
    return '产品版本' in t


def clean(s):
    s = (s or '').replace('\n', ' ').replace('|', '\\|')
    return re.sub(r'\s+', ' ', s).strip()


def inside_box(line, bbox):
    x0, top, x1, bottom = bbox
    return (line['top'] >= top - 2 and line['bottom'] <= bottom + 2
            and line['x0'] >= x0 - 2 and line['x1'] <= x1 + 2)


def is_header_row(cells):
    return len(cells) >= 4 and ('类型' in cells) and ('描述' in cells)


def is_viewlist_header(cells):
    return len(cells) >= 2 and '视图名' in cells and '功能描述' in cells


def table_kind(rows):
    """Return 'field' (4-col), 'viewlist' (2-col) or None for a detected table."""
    if not rows or not any(rows[0]):
        return None
    r0 = [clean(c) for c in rows[0]]
    if is_header_row(r0):
        return 'field'
    if is_viewlist_header(r0):
        return 'viewlist'
    # a continuation page of a field table: first row is a data row (NO/YES)
    if len(r0) >= 4 and r0[2] in ('NO', 'YES') and r0[1]:
        return 'field'
    return None


def is_field_table(rows):
    return table_kind(rows) is not None


def is_code_line(t):
    t = t.strip()
    if not t:
        return False
    return bool(CODE_RE.match(t)) or bool(GV_ROW_RE.search(t) and not CJK_RE.match(t))


def build_pages(pdf, start, end):
    """start/end are 0-indexed page numbers (inclusive)."""
    pages = {}
    for pi in range(start, end + 1):
        pa = pdf.pages[pi]
        raw = [l for l in pa.extract_text_lines()
               if l['text'].strip() and not is_noise(l['text'])]
        field_tables = []
        for t in pa.find_tables():
            rows = t.extract()
            if rows and is_field_table(rows):
                field_tables.append((t.bbox, rows))
        header_key = None
        name_skip = set()
        if raw:
            first = raw[0]['text'].strip()
            m = VIEW_RE.match(first)
            base = 1
            num = name = None
            if m:
                num, name = m.group(1), m.group(2)
            else:
                # heading wrapped across lines: "786" alone, name below
                m2 = re.match(r'^(\d+)$', first)
                if m2 and len(raw) > 1 and re.fullmatch(r'[A-Za-z0-9_.$]+', raw[1]['text'].strip()):
                    num, name, base = m2.group(1), raw[1]['text'].strip(), 2
            if num and not num.startswith('0') and any(
                    l['text'].strip().startswith(num + '.') for l in raw):
                if base == 2:
                    name_skip.add(name)
                NAME_FRAG = re.compile(r'[A-Za-z0-9_.$]+')
                for l in raw[base:]:
                    txt = l['text'].strip()
                    if txt.startswith(num + '.'):
                        break
                    # view names are ASCII identifiers; stop at prose (CJK or spaces)
                    if not NAME_FRAG.fullmatch(txt):
                        break
                    name_skip.add(txt)
                    name += txt  # names wrap mid-token -> concatenate
                header_key = (num, name)
        pages[pi] = {
            'raw': raw,
            'field_tables': field_tables,
            'header_key': header_key,
            'name_skip': name_skip,
        }
    return pages


def render(pages):
    out = []
    para = []
    code = []
    code_mode = False

    def flush_para():
        nonlocal para
        if not para:
            return
        p = ''
        for ln in para:
            if not p:
                p = ln
            else:
                a, b = p[-1], ln[0]
                p += ('' if not a.isascii() or not b.isascii() else ' ') + ln
        out.append(p)
        para = []

    def flush_code():
        nonlocal code
        if not code:
            return
        out.append('```')
        out.extend(code)
        out.append('```')
        code = []

    def emit_field_table(rows):
        rows = [r for r in rows if r and any(c for c in r)]
        # drop rows polluted by page watermarks sometimes captured by find_tables
        rows = [r for r in rows
                if not any('产品版本' in (c or '') or (c or '').strip().startswith('OceanBase 数据库')
                           for c in r if c)]
        if not rows:
            return
        header = [clean(c) for c in rows[0]]
        is_hdr = is_header_row(header) or is_viewlist_header(header)
        data_rows = rows[1:] if is_hdr else rows
        if is_hdr:
            out.append('| ' + ' | '.join(header) + ' |')
            out.append('|' + '|'.join('---' for _ in header) + '|')
        for row in data_rows:
            cells = [clean(c) for c in row]
            while len(cells) < len(header):
                cells.append('')
            out.append('| ' + ' | '.join(cells) + ' |')

    for pi in sorted(pages):
        info = pages[pi]
        hk = info['header_key']
        name_skip = info['name_skip']

        def header_event(txt):
            t = txt.strip()
            m = VIEW_RE.match(t) or re.match(r'^(\d+)$', t)
            return m and not m.group(1).startswith('0') and hk is not None and m.group(1) == hk[0]

        events = []
        for bbox, rows in info['field_tables']:
            events.append(('table', bbox[1], rows))
        for line in info['raw']:
            if line['text'].strip() in name_skip:
                continue
            if any(inside_box(line, bbox) for bbox, _ in info['field_tables']):
                continue
            events.append(('text', line['top'], line['text'].strip()))

        for ev in sorted(events, key=lambda e: e[1]):
            if ev[0] == 'table':
                flush_para()
                flush_code()
                emit_field_table(ev[2])
                code_mode = False
                continue
            txt = ev[2]
            if header_event(txt):
                flush_para()
                flush_code()
                num, name = hk
                out.append(f'\n## {num} {name}\n')
                code_mode = False
                continue
            if VIEW_RE.match(txt):
                continue
            ms = SUB_RE.match(txt)
            if ms:
                flush_para()
                flush_code()
                num, title = ms.group(1), ms.group(2).strip()
                lvl = 3 if num.count('.') <= 1 else 4
                out.append(f'{"#" * lvl} {num} {title}\n')
                code_mode = False
                continue
            if CJK_RE.match(txt):
                flush_code()
                code_mode = False
                para.append(txt)
            elif code_mode:
                code.append(txt)
            elif is_code_line(txt):
                flush_para()
                code.append(txt)
                code_mode = True
            else:
                para.append(txt)
    flush_para()
    flush_code()
    return '\n'.join(out) + '\n'


def main(argv):
    ap = argparse.ArgumentParser(description='Extract OceanBase sys-view PDF section -> Markdown')
    ap.add_argument('pdf')
    ap.add_argument('start_page', type=int, help='1-indexed inclusive first page')
    ap.add_argument('end_page', type=int, help='1-indexed inclusive last page')
    ap.add_argument('out')
    args = ap.parse_args(argv)

    with pdfplumber.open(args.pdf) as pdf:
        pages = build_pages(pdf, args.start_page - 1, args.end_page - 1)
    text = render(pages)
    with open(args.out, 'w', encoding='utf-8') as f:
        f.write(text)
    heads = re.findall(r'^## (\d+) ', text, re.M)
    print(f'wrote {args.out}: views={len(heads)} '
          f'first={heads[0] if heads else None} last={heads[-1] if heads else None} '
          f'chars={len(text)}')


if __name__ == '__main__':
    main(sys.argv[1:])
