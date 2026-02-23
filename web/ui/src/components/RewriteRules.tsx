import { useCallback, useEffect, useState } from "react";
import {
  type RewriteRule,
  type RewriteRuleInput,
  type RewriteTestResult,
  fetchRewriteRules,
  createRewriteRule,
  updateRewriteRule,
  deleteRewriteRule,
  toggleRewriteRule,
  testRewritePattern,
} from "../api";

const emptyRule: RewriteRuleInput = {
  name: "",
  pattern: "",
  replacement: "",
  is_regex: false,
  domains: [],
  url_patterns: [],
  content_types: [],
  enabled: true,
};

export default function RewriteRules() {
  const [rules, setRules] = useState<RewriteRule[]>([]);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<string | null>(null); // rule ID or "new"
  const [form, setForm] = useState<RewriteRuleInput>(emptyRule);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);

  const loadRules = useCallback(async () => {
    try {
      const r = await fetchRewriteRules();
      setRules(r);
      setError("");
    } catch (e: unknown) {
      setError((e as Error).message);
    }
  }, []);

  useEffect(() => {
    void loadRules();
  }, [loadRules]);

  function startNew() {
    setForm(emptyRule);
    setEditing("new");
  }

  function startEdit(rule: RewriteRule) {
    setForm({
      name: rule.name,
      pattern: rule.pattern,
      replacement: rule.replacement,
      is_regex: rule.is_regex,
      domains: rule.domains,
      url_patterns: rule.url_patterns,
      content_types: rule.content_types,
      enabled: rule.enabled,
    });
    setEditing(rule.id);
  }

  function cancelEdit() {
    setEditing(null);
    setForm(emptyRule);
  }

  async function handleSave() {
    setSaving(true);
    try {
      if (editing === "new") {
        await createRewriteRule(form);
      } else if (editing) {
        await updateRewriteRule(editing, form);
      }
      setEditing(null);
      setForm(emptyRule);
      await loadRules();
      setError("");
    } catch (e: unknown) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete(id: string) {
    setDeleting(id);
    try {
      await deleteRewriteRule(id);
      await loadRules();
      setError("");
    } catch (e: unknown) {
      setError((e as Error).message);
    } finally {
      setDeleting(null);
    }
  }

  async function handleToggle(id: string) {
    try {
      await toggleRewriteRule(id);
      await loadRules();
    } catch (e: unknown) {
      setError((e as Error).message);
    }
  }

  return (
    <div className="space-y-4">
      {error && (
        <div className="text-xs p-2 rounded border border-vsc-error/50 text-vsc-error bg-vsc-error/10">
          {error}
        </div>
      )}

      <div className="flex items-center justify-between">
        <span className="text-xs text-vsc-muted">
          {rules.length} rule{rules.length !== 1 ? "s" : ""}
        </span>
        {!editing && (
          <button
            onClick={startNew}
            className="text-xs bg-vsc-accent/20 border border-vsc-accent/40 rounded px-3 py-1 text-vsc-accent hover:bg-vsc-accent/30 transition-colors"
          >
            + New Rule
          </button>
        )}
      </div>

      {editing && (
        <RuleForm
          form={form}
          setForm={setForm}
          onSave={handleSave}
          onCancel={cancelEdit}
          saving={saving}
          isNew={editing === "new"}
        />
      )}

      {rules.length === 0 && !editing && (
        <p className="text-xs text-vsc-muted text-center py-8">
          No rewrite rules configured. Click "+ New Rule" to create one.
        </p>
      )}

      <div className="space-y-2">
        {rules.map((rule) => (
          <RuleCard
            key={rule.id}
            rule={rule}
            onEdit={() => startEdit(rule)}
            onDelete={() => handleDelete(rule.id)}
            onToggle={() => handleToggle(rule.id)}
            deleting={deleting === rule.id}
            isEditing={editing === rule.id}
          />
        ))}
      </div>
    </div>
  );
}

interface RuleFormProps {
  form: RewriteRuleInput;
  setForm: (f: RewriteRuleInput) => void;
  onSave: () => void;
  onCancel: () => void;
  saving: boolean;
  isNew: boolean;
}

function RuleForm({ form, setForm, onSave, onCancel, saving, isNew }: RuleFormProps) {
  const [testSample, setTestSample] = useState("");
  const [testResult, setTestResult] = useState<RewriteTestResult | null>(null);
  const [testing, setTesting] = useState(false);
  const [domainsText, setDomainsText] = useState(form.domains.join(", "));
  const [urlPatternsText, setUrlPatternsText] = useState(form.url_patterns.join(", "));
  const [contentTypesText, setContentTypesText] = useState(form.content_types.join(", "));

  async function handleTest() {
    if (!form.pattern || !testSample) return;
    setTesting(true);
    try {
      const result = await testRewritePattern(
        form.pattern,
        form.replacement,
        form.is_regex,
        testSample,
      );
      setTestResult(result);
    } catch (e: unknown) {
      setTestResult({ result: "", match_count: 0, valid: false, error: (e as Error).message });
    } finally {
      setTesting(false);
    }
  }

  function updateDomains(text: string) {
    setDomainsText(text);
    const domains = text
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    setForm({ ...form, domains });
  }

  function updateUrlPatterns(text: string) {
    setUrlPatternsText(text);
    const patterns = text
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    setForm({ ...form, url_patterns: patterns });
  }

  function updateContentTypes(text: string) {
    setContentTypesText(text);
    const types = text
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    setForm({ ...form, content_types: types });
  }

  return (
    <div className="bg-vsc-surface border border-vsc-accent/30 rounded p-4 space-y-3">
      <h3 className="text-xs text-vsc-accent font-bold">
        {isNew ? "New Rule" : "Edit Rule"}
      </h3>

      <div className="grid grid-cols-1 gap-3">
        <label className="block">
          <span className="text-xs text-vsc-muted">Name</span>
          <input
            type="text"
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            placeholder="Rule name"
            className="mt-1 w-full bg-vsc-bg border border-vsc-border rounded px-2 py-1 text-xs text-vsc-fg focus:border-vsc-accent outline-none"
          />
        </label>

        <div className="grid grid-cols-2 gap-3">
          <label className="block">
            <span className="text-xs text-vsc-muted">Pattern</span>
            <input
              type="text"
              value={form.pattern}
              onChange={(e) => setForm({ ...form, pattern: e.target.value })}
              placeholder={form.is_regex ? "\\bfoo\\b" : "foo"}
              className="mt-1 w-full bg-vsc-bg border border-vsc-border rounded px-2 py-1 text-xs text-vsc-fg font-mono focus:border-vsc-accent outline-none"
            />
          </label>

          <label className="block">
            <span className="text-xs text-vsc-muted">Replacement</span>
            <input
              type="text"
              value={form.replacement}
              onChange={(e) => setForm({ ...form, replacement: e.target.value })}
              placeholder="bar (empty = delete)"
              className="mt-1 w-full bg-vsc-bg border border-vsc-border rounded px-2 py-1 text-xs text-vsc-fg font-mono focus:border-vsc-accent outline-none"
            />
          </label>
        </div>

        <div className="flex items-center gap-4">
          <label className="flex items-center gap-2 text-xs text-vsc-muted cursor-pointer">
            <input
              type="checkbox"
              checked={form.is_regex}
              onChange={(e) => setForm({ ...form, is_regex: e.target.checked })}
              className="accent-vsc-accent"
            />
            Regex
          </label>
          <label className="flex items-center gap-2 text-xs text-vsc-muted cursor-pointer">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
              className="accent-vsc-accent"
            />
            Enabled
          </label>
        </div>

        <div className="grid grid-cols-2 gap-3">
          <label className="block">
            <span className="text-xs text-vsc-muted">Domains (comma-separated, empty = all)</span>
            <input
              type="text"
              value={domainsText}
              onChange={(e) => updateDomains(e.target.value)}
              placeholder="example.com, test.com"
              className="mt-1 w-full bg-vsc-bg border border-vsc-border rounded px-2 py-1 text-xs text-vsc-fg focus:border-vsc-accent outline-none"
            />
          </label>

          <label className="block">
            <span className="text-xs text-vsc-muted">URL patterns (comma-separated, empty = all)</span>
            <input
              type="text"
              value={urlPatternsText}
              onChange={(e) => updateUrlPatterns(e.target.value)}
              placeholder="/blog/*, /api/*"
              className="mt-1 w-full bg-vsc-bg border border-vsc-border rounded px-2 py-1 text-xs text-vsc-fg focus:border-vsc-accent outline-none"
            />
          </label>
        </div>

        <label className="block">
          <span className="text-xs text-vsc-muted">
            Content types (comma-separated, empty = text/html + text/plain only)
          </span>
          <input
            type="text"
            value={contentTypesText}
            onChange={(e) => updateContentTypes(e.target.value)}
            placeholder="text/html, text/plain (default if empty)"
            className="mt-1 w-full bg-vsc-bg border border-vsc-border rounded px-2 py-1 text-xs text-vsc-fg focus:border-vsc-accent outline-none"
          />
        </label>

        {/* Test section */}
        <div className="border-t border-vsc-border pt-3 space-y-2">
          <span className="text-xs text-vsc-muted">Test Pattern</span>
          <div className="flex gap-2">
            <input
              type="text"
              value={testSample}
              onChange={(e) => setTestSample(e.target.value)}
              placeholder="Enter sample text to test against"
              className="flex-1 bg-vsc-bg border border-vsc-border rounded px-2 py-1 text-xs text-vsc-fg font-mono focus:border-vsc-accent outline-none"
            />
            <button
              onClick={handleTest}
              disabled={testing || !form.pattern || !testSample}
              className="text-xs bg-vsc-surface border border-vsc-border rounded px-3 py-1 text-vsc-accent hover:bg-vsc-header disabled:opacity-50 transition-colors"
            >
              {testing ? "..." : "Test"}
            </button>
          </div>
          {testResult && (
            <div
              className={`text-xs p-2 rounded border font-mono ${
                testResult.valid
                  ? "border-vsc-border bg-vsc-bg text-vsc-fg"
                  : "border-vsc-error/50 bg-vsc-error/10 text-vsc-error"
              }`}
            >
              {testResult.valid ? (
                <>
                  <div>
                    <span className="text-vsc-muted">Matches:</span> {testResult.match_count}
                  </div>
                  <div>
                    <span className="text-vsc-muted">Result:</span> {testResult.result}
                  </div>
                </>
              ) : (
                <div>{testResult.error}</div>
              )}
            </div>
          )}
        </div>
      </div>

      <div className="flex gap-2 pt-2">
        <button
          onClick={onSave}
          disabled={saving || !form.name || !form.pattern}
          className="text-xs bg-vsc-accent/20 border border-vsc-accent/40 rounded px-4 py-1 text-vsc-accent hover:bg-vsc-accent/30 disabled:opacity-50 transition-colors"
        >
          {saving ? "Saving..." : "Save"}
        </button>
        <button
          onClick={onCancel}
          className="text-xs bg-vsc-surface border border-vsc-border rounded px-4 py-1 text-vsc-muted hover:text-vsc-fg transition-colors"
        >
          Cancel
        </button>
      </div>
    </div>
  );
}

interface RuleCardProps {
  rule: RewriteRule;
  onEdit: () => void;
  onDelete: () => void;
  onToggle: () => void;
  deleting: boolean;
  isEditing: boolean;
}

function RuleCard({ rule, onEdit, onDelete, onToggle, deleting, isEditing }: RuleCardProps) {
  const [confirmDelete, setConfirmDelete] = useState(false);

  if (isEditing) return null;

  return (
    <div
      className={`bg-vsc-surface border rounded p-3 ${
        rule.enabled ? "border-vsc-border" : "border-vsc-border/50 opacity-60"
      }`}
    >
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 min-w-0">
          <button
            onClick={onToggle}
            className={`w-8 h-4 rounded-full relative transition-colors ${
              rule.enabled ? "bg-vsc-accent/60" : "bg-vsc-border"
            }`}
            title={rule.enabled ? "Disable" : "Enable"}
          >
            <span
              className={`absolute top-0.5 w-3 h-3 rounded-full bg-white transition-all ${
                rule.enabled ? "left-4" : "left-0.5"
              }`}
            />
          </button>
          <span className="text-xs font-medium text-vsc-fg truncate">{rule.name}</span>
          {rule.is_regex && (
            <span className="text-[10px] px-1 rounded bg-vsc-accent/20 text-vsc-accent">
              regex
            </span>
          )}
        </div>
        <div className="flex items-center gap-1 shrink-0">
          <button
            onClick={onEdit}
            className="text-xs px-2 py-0.5 text-vsc-muted hover:text-vsc-accent transition-colors"
          >
            Edit
          </button>
          {confirmDelete ? (
            <div className="flex items-center gap-1">
              <button
                onClick={() => {
                  onDelete();
                  setConfirmDelete(false);
                }}
                disabled={deleting}
                className="text-xs px-2 py-0.5 text-vsc-error hover:text-vsc-error/80 transition-colors"
              >
                {deleting ? "..." : "Confirm"}
              </button>
              <button
                onClick={() => setConfirmDelete(false)}
                className="text-xs px-2 py-0.5 text-vsc-muted hover:text-vsc-fg transition-colors"
              >
                Cancel
              </button>
            </div>
          ) : (
            <button
              onClick={() => setConfirmDelete(true)}
              className="text-xs px-2 py-0.5 text-vsc-muted hover:text-vsc-error transition-colors"
            >
              Delete
            </button>
          )}
        </div>
      </div>
      <div className="mt-2 text-xs text-vsc-muted font-mono truncate">
        <span className="text-vsc-fg">{rule.pattern}</span>
        {rule.replacement ? (
          <>
            {" -> "}
            <span className="text-vsc-accent">{rule.replacement}</span>
          </>
        ) : (
          <span className="text-vsc-error"> (delete)</span>
        )}
      </div>
      {(rule.domains.length > 0 || rule.url_patterns.length > 0 || rule.content_types.length > 0) && (
        <div className="mt-1 flex gap-3 text-[10px] text-vsc-muted">
          {rule.domains.length > 0 && <span>Domains: {rule.domains.join(", ")}</span>}
          {rule.url_patterns.length > 0 && <span>URLs: {rule.url_patterns.join(", ")}</span>}
          {rule.content_types.length > 0 && <span>Types: {rule.content_types.join(", ")}</span>}
        </div>
      )}
    </div>
  );
}
