package resume

import (
	"context"
	"fmt"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

type Exporter struct{}

func NewExporter() *Exporter {
	return &Exporter{}
}

// ExportPDF 将HTML字符串渲染为A4 PDF
func (e *Exporter) ExportPDF(ctx context.Context, html string) ([]byte, error) {
	// 创建headless Chrome实例
	allocCtx, cancel := chromedp.NewExecAllocator(ctx,
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("disable-gpu", true),
		)...,
	)
	defer cancel()

	taskCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	var pdfBuf []byte

	err := chromedp.Run(taskCtx,
		// 加载HTML内容
		chromedp.Navigate("about:blank"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			frameTree, err := page.GetFrameTree().Do(ctx)
			if err != nil {
				return err
			}
			return page.SetDocumentContent(frameTree.Frame.ID, html).Do(ctx)
		}),
		// 等待渲染完成
		chromedp.WaitReady("body"),
		// 生成PDF
		chromedp.ActionFunc(func(ctx context.Context) error {
			buf, _, err := page.PrintToPDF().
				WithPaperWidth(8.27).   // A4宽度 (210mm = 8.27in)
				WithPaperHeight(11.69). // A4高度 (297mm = 11.69in)
				WithMarginTop(0).
				WithMarginBottom(0).
				WithMarginLeft(0).
				WithMarginRight(0).
				WithPrintBackground(true).
				Do(ctx)
			if err != nil {
				return err
			}
			pdfBuf = buf
			return nil
		}),
	)

	if err != nil {
		return nil, fmt.Errorf("export pdf: %w", err)
	}

	return pdfBuf, nil
}
