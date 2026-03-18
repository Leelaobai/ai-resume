#!/usr/bin/env python3
"""从PDF或DOCX文件提取纯文本"""
import sys
import os

def parse_pdf(filepath):
    import pdfplumber
    text = []
    with pdfplumber.open(filepath) as pdf:
        for page in pdf.pages:
            t = page.extract_text()
            if t:
                text.append(t)
    return '\n'.join(text)

def parse_docx(filepath):
    import docx
    doc = docx.Document(filepath)
    return '\n'.join([p.text for p in doc.paragraphs if p.text.strip()])

if __name__ == '__main__':
    if len(sys.argv) != 2:
        print("Usage: parse_resume.py <filepath>", file=sys.stderr)
        sys.exit(1)

    filepath = sys.argv[1]
    ext = os.path.splitext(filepath)[1].lower()

    if ext == '.pdf':
        print(parse_pdf(filepath))
    elif ext in ('.docx', '.doc'):
        print(parse_docx(filepath))
    else:
        print(f"Unsupported file type: {ext}", file=sys.stderr)
        sys.exit(1)