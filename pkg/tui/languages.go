package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/locales"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func (m *Model) getLocalesMgr() *locales.Manager {
	if m.localesMgr == nil {
		m.localesMgr = locales.NewManagerWithCacheDir(locales.JoinCacheDir(config.GetLeleDir()))
	}
	return m.localesMgr
}

func (m *Model) openRemoteLanguageBrowser() tea.Cmd {
	m.langInstallMsg = ""
	m.langInstallBusy = true
	m.modalItems = []string{i18n.T("tui.languages.loadingCatalog")}
	m.modalLangCodes = nil
	m.modalSelectedIdx = 0
	m.modalMode = ModalLangRemote

	mgr := m.getLocalesMgr()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cat, err := mgr.RefreshCatalog(ctx)
		if err != nil && cat == nil {
			return langCatalogMsg{err: err}
		}
		list := mgr.List(ctx)
		return langCatalogMsg{statuses: list}
	}
}

type langCatalogMsg struct {
	statuses []locales.Status
	err      error
}

type langInstallResultMsg struct {
	code string
	err  error
}

func (m *Model) handleLangCatalogMsg(msg langCatalogMsg) tea.Cmd {
	m.langInstallBusy = false
	if msg.err != nil && len(msg.statuses) == 0 {
		m.langInstallMsg = i18n.T("tui.languages.catalogFailed")
		m.modalItems = []string{m.langInstallMsg + " — " + msg.err.Error()}
		m.modalLangCodes = nil
		m.modalMode = ModalLang
		m.modalSelectedIdx = 0
		m.loadLanguageSettings()
		return nil
	}
	labels, codes := remoteLanguageLabels(msg.statuses)
	if len(labels) == 0 {
		m.modalItems = []string{i18n.T("tui.languages.allInstalled")}
		m.modalLangCodes = nil
		m.modalMode = ModalLang
		m.modalSelectedIdx = 0
		m.loadLanguageSettings()
		return nil
	}
	m.modalItems = labels
	m.modalLangCodes = codes
	m.modalSelectedIdx = 0
	m.modalScrollOffset = 0
	return nil
}

func (m *Model) handleLangInstallSelect() tea.Cmd {
	if m.modalSelectedIdx >= len(m.modalLangCodes) {
		return nil
	}
	code := m.modalLangCodes[m.modalSelectedIdx]
	if code == "" {
		return nil
	}
	m.langInstallBusy = true
	m.langInstallMsg = i18n.T("tui.languages.downloading")
	mgr := m.getLocalesMgr()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		err := mgr.Install(ctx, code)
		return langInstallResultMsg{code: code, err: err}
	}
}

func (m *Model) handleLangInstallResult(msg langInstallResultMsg) tea.Cmd {
	m.langInstallBusy = false
	if msg.err != nil {
		m.langInstallMsg = i18n.T("tui.languages.downloadFailed") + ": " + msg.err.Error()
		return nil
	}
	i18n.LoadExternalPacks()
	i18n.RegisterExternal(msg.code, loadPackMap(m.getLocalesMgr(), msg.code))
	m.cfg.Language = msg.code
	m.saveConfigToDisk()
	i18n.SetLanguage(msg.code)
	m.langInstallMsg = i18n.T("tui.languages.downloadOK")
	m.chatInput.Placeholder = i18n.T("tui.placeholder")
	m.modalMode = ModalLang
	m.modalSelectedIdx = 0
	m.loadLanguageSettings()
	m.lastViewportKey = ""
	m.renderedBaseKey = ""
	return nil
}

func loadPackMap(mgr *locales.Manager, code string) map[string]string {
	tui, err := mgr.GetTUI(code, nil)
	if err != nil {
		return nil
	}
	return tui
}

func (m *Model) ensureLocalePack(code string) error {
	code = locales.NormalizeCode(code)
	if code == "es" || code == "en" || code == "pt" {
		return nil
	}
	mgr := m.getLocalesMgr()
	if mgr.IsInstalled(code) {
		i18n.LoadExternalPacks()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := mgr.Install(ctx, code); err != nil {
		return err
	}
	i18n.LoadExternalPacks()
	if tui, err := mgr.GetTUI(code, nil); err == nil {
		i18n.RegisterExternal(code, tui)
	}
	return nil
}

func (m *Model) applyLanguage(code string) error {
	if err := m.ensureLocalePack(code); err != nil {
		return err
	}
	m.cfg.Language = code
	m.saveConfigToDisk()
	i18n.SetLanguage(code)
	m.chatInput.Placeholder = i18n.T("tui.placeholder")
	return nil
}

func (m *Model) renderLangRemoteHint() string {
	if m.langInstallBusy {
		return i18n.T("tui.languages.downloading")
	}
	if m.langInstallMsg != "" {
		return m.langInstallMsg
	}
	return i18n.T("tui.languages.remoteHint")
}

func formatLangFeedback(code string, err error) string {
	if err != nil {
		return fmt.Sprintf("%s (%s): %v", i18n.T("tui.languages.downloadFailed"), code, err)
	}
	return i18n.T("tui.languages.downloadOK")
}
