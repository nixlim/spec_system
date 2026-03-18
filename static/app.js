// ==========================================================================
// Adversarial Spec System — Dashboard Application
// Vanilla JS, no external dependencies.
// ==========================================================================

(function () {
  "use strict";

  // -----------------------------------------------------------------------
  // Configuration
  // -----------------------------------------------------------------------

  var RECONNECT_BASE_MS = 1000;
  var RECONNECT_MAX_MS = 30000;
  var ALLOWED_EXTENSIONS = [".md", ".txt", ".pdf", ".go", ".ts", ".py", ".js", ".yaml", ".json"];

  // -----------------------------------------------------------------------
  // State
  // -----------------------------------------------------------------------

  var ws = null;
  var reconnectAttempt = 0;
  var reconnectTimer = null;
  var workflowActive = false; // tracks whether a non-idle workflow is displayed

  // Cache for issues, convergence history, etc.
  var issueCache = [];
  var convergenceHistory = [];
  var lensSet = new Set();

  // Gate state
  var gate1CorrectionCount = 0;
  var gate2AnswerDisabled = false;

  // Workflow poller — periodically checks for state changes during active runs
  var workflowPoller = null;

  // Messages tab state
  var messagesPollingTimer = null;
  var lastServerLogCount = 0;

  // -----------------------------------------------------------------------
  // DOM helpers
  // -----------------------------------------------------------------------

  function $(sel, ctx) { return (ctx || document).querySelector(sel); }
  function $$(sel, ctx) { return Array.from((ctx || document).querySelectorAll(sel)); }
  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    if (attrs) {
      Object.keys(attrs).forEach(function (k) {
        if (k === "className") node.className = attrs[k];
        else if (k === "textContent") node.textContent = attrs[k];
        else if (k === "innerHTML") node.innerHTML = attrs[k];
        else if (k.startsWith("on")) node.addEventListener(k.slice(2).toLowerCase(), attrs[k]);
        else node.setAttribute(k, attrs[k]);
      });
    }
    if (children) {
      (Array.isArray(children) ? children : [children]).forEach(function (c) {
        if (typeof c === "string") node.appendChild(document.createTextNode(c));
        else if (c) node.appendChild(c);
      });
    }
    return node;
  }

  function clearChildren(node) {
    while (node.firstChild) node.removeChild(node.firstChild);
  }

  // Simple string hash for stable keying (returns e.g. "q1a2b3c").
  function simpleHash(str) {
    var hash = 0;
    for (var i = 0; i < str.length; i++) {
      hash = ((hash << 5) - hash) + str.charCodeAt(i);
      hash |= 0; // Convert to 32-bit integer
    }
    return "q" + Math.abs(hash).toString(36);
  }

  // -----------------------------------------------------------------------
  // Markdown → HTML (basic, no external libs)
  // -----------------------------------------------------------------------

  function renderMarkdown(md) {
    if (!md) return "<p><em>No content</em></p>";

    var html = "";
    var lines = md.split("\n");
    var inCodeBlock = false;
    var codeBuffer = [];
    var inList = false;
    var listType = "";

    for (var i = 0; i < lines.length; i++) {
      var line = lines[i];

      // Fenced code blocks
      if (/^```/.test(line)) {
        if (inCodeBlock) {
          html += "<pre><code>" + escapeHtml(codeBuffer.join("\n")) + "</code></pre>\n";
          codeBuffer = [];
          inCodeBlock = false;
        } else {
          if (inList) { html += listType === "ul" ? "</ul>\n" : "</ol>\n"; inList = false; }
          inCodeBlock = true;
        }
        continue;
      }
      if (inCodeBlock) {
        codeBuffer.push(line);
        continue;
      }

      // Headers
      var headerMatch = line.match(/^(#{1,6})\s+(.*)/);
      if (headerMatch) {
        if (inList) { html += listType === "ul" ? "</ul>\n" : "</ol>\n"; inList = false; }
        var level = headerMatch[1].length;
        html += "<h" + level + ">" + inlineFormat(headerMatch[2]) + "</h" + level + ">\n";
        continue;
      }

      // Unordered list
      if (/^\s*[-*+]\s+/.test(line)) {
        if (!inList || listType !== "ul") {
          if (inList) html += listType === "ul" ? "</ul>\n" : "</ol>\n";
          html += "<ul>\n";
          inList = true;
          listType = "ul";
        }
        html += "<li>" + inlineFormat(line.replace(/^\s*[-*+]\s+/, "")) + "</li>\n";
        continue;
      }

      // Ordered list
      if (/^\s*\d+\.\s+/.test(line)) {
        if (!inList || listType !== "ol") {
          if (inList) html += listType === "ul" ? "</ul>\n" : "</ol>\n";
          html += "<ol>\n";
          inList = true;
          listType = "ol";
        }
        html += "<li>" + inlineFormat(line.replace(/^\s*\d+\.\s+/, "")) + "</li>\n";
        continue;
      }

      // Close list if we're not in one
      if (inList && line.trim() === "") {
        html += listType === "ul" ? "</ul>\n" : "</ol>\n";
        inList = false;
        continue;
      }

      // Blank line
      if (line.trim() === "") {
        continue;
      }

      // Paragraph
      if (inList) { html += listType === "ul" ? "</ul>\n" : "</ol>\n"; inList = false; }
      html += "<p>" + inlineFormat(line) + "</p>\n";
    }

    // Close open blocks
    if (inCodeBlock) html += "<pre><code>" + escapeHtml(codeBuffer.join("\n")) + "</code></pre>\n";
    if (inList) html += listType === "ul" ? "</ul>\n" : "</ol>\n";

    return html;
  }

  function inlineFormat(text) {
    text = escapeHtml(text);
    // Bold
    text = text.replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>");
    text = text.replace(/__(.+?)__/g, "<strong>$1</strong>");
    // Italic
    text = text.replace(/\*(.+?)\*/g, "<em>$1</em>");
    text = text.replace(/_(.+?)_/g, "<em>$1</em>");
    // Inline code
    text = text.replace(/`(.+?)`/g, "<code>$1</code>");
    return text;
  }

  function escapeHtml(s) {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
  }

  // -----------------------------------------------------------------------
  // Badges
  // -----------------------------------------------------------------------

  function severityBadge(sev) {
    var s = (sev || "").toLowerCase();
    return '<span class="badge badge-' + s + '">' + escapeHtml(sev) + "</span>";
  }

  function statusBadge(status) {
    var s = (status || "").toLowerCase();
    return '<span class="badge badge-' + s + '">' + escapeHtml(status) + "</span>";
  }

  function verdictBadge(verdict) {
    var v = (verdict || "").toLowerCase();
    return '<span class="badge badge-' + v + '">' + escapeHtml(verdict) + "</span>";
  }

  // -----------------------------------------------------------------------
  // Format helpers
  // -----------------------------------------------------------------------

  function formatBytes(bytes) {
    if (bytes < 1024) return bytes + " B";
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KB";
    return (bytes / (1024 * 1024)).toFixed(1) + " MB";
  }

  function formatDate(iso) {
    if (!iso) return "-";
    var d = new Date(iso);
    if (isNaN(d.getTime())) return iso;
    return d.toLocaleDateString() + " " + d.toLocaleTimeString();
  }

  function formatCost(usd) {
    if (usd == null || isNaN(usd)) return "$0.00";
    return "$" + Number(usd).toFixed(2);
  }

  function formatDuration(seconds) {
    if (seconds == null || isNaN(seconds)) return "0s";
    seconds = Math.round(seconds);
    if (seconds < 60) return seconds + "s";
    if (seconds < 3600) {
      var m = Math.floor(seconds / 60);
      var s = seconds % 60;
      return m + "m " + s + "s";
    }
    var h = Math.floor(seconds / 3600);
    var m = Math.floor((seconds % 3600) / 60);
    var s = seconds % 60;
    return h + "h " + m + "m " + s + "s";
  }

  function formatTime(isoString) {
    if (!isoString) return "";
    var d = new Date(isoString);
    if (isNaN(d.getTime())) return "";
    var hh = String(d.getHours()).padStart(2, "0");
    var mm = String(d.getMinutes()).padStart(2, "0");
    var ss = String(d.getSeconds()).padStart(2, "0");
    return hh + ":" + mm + ":" + ss;
  }

  // -----------------------------------------------------------------------
  // Workflow Status Panel
  // -----------------------------------------------------------------------

  var ACTIVITY_MAX_ENTRIES = 50;

  function getStateBadgeClass(state) {
    var s = (state || "").toUpperCase();
    switch (s) {
      case "INIT":
      case "DISCOVERY":
      case "DRAFTING":
        return "state-badge-blue";
      case "REVIEWING":
      case "REVISING":
      case "JUDGING":
        return "state-badge-orange";
      case "HUMAN_GATE_1":
      case "HUMAN_GATE_2":
      case "HUMAN_GATE_FINAL":
        return "state-badge-purple";
      case "FINALIZED":
        return "state-badge-green";
      case "ESCALATED":
      case "ERROR":
        return "state-badge-red";
      default:
        return "state-badge-idle";
    }
  }

  // Client-side wall clock timer — ticks every second so the TIME display
  // updates live without needing a server round-trip.
  var wallClockTimer = null;
  var wallClockSeconds = 0;

  function startWallClockTimer() {
    if (wallClockTimer) return; // already running
    wallClockTimer = setInterval(function () {
      wallClockSeconds++;
      $("#status-time").textContent = formatDuration(wallClockSeconds);
    }, 1000);
  }

  function stopWallClockTimer() {
    if (wallClockTimer) {
      clearInterval(wallClockTimer);
      wallClockTimer = null;
    }
  }

  function updateWorkflowStatus(data) {
    var panel = $("#workflow-status");
    panel.hidden = false;

    var badge = $("#workflow-state");
    var state = data.state || "IDLE";
    badge.textContent = state;
    badge.className = "state-badge " + getStateBadgeClass(state);

    // Track whether a workflow is active (non-idle) so pollWorkflowStatus
    // doesn't overwrite with stale "idle" responses during startup.
    workflowActive = state.toUpperCase() !== "IDLE";

    if (data.feature_name != null) {
      $("#status-feature").textContent = data.feature_name || "-";
    }
    if (data.round != null) {
      $("#status-round").textContent = data.round;
    }
    if (data.cost_usd != null) {
      $("#status-cost").textContent = formatCost(data.cost_usd);
    }
    if (data.wall_clock_seconds != null) {
      wallClockSeconds = Math.round(data.wall_clock_seconds);
      $("#status-time").textContent = formatDuration(wallClockSeconds);
    }
    if (data.agent_invocations != null) {
      $("#status-invocations").textContent = data.agent_invocations;
    }

    // Start or stop the client-side timer based on workflow state.
    var upper = state.toUpperCase();
    if (upper !== "IDLE" && upper !== "FINALIZED" && upper !== "ESCALATED") {
      startWallClockTimer();
    } else {
      stopWallClockTimer();
    }
  }

  function addActivityEntry(message, type) {
    var container = $("#status-activity");
    var typeClass = type ? "activity-" + type : "";

    var now = new Date();
    var timestamp = String(now.getHours()).padStart(2, "0") + ":" +
                    String(now.getMinutes()).padStart(2, "0") + ":" +
                    String(now.getSeconds()).padStart(2, "0");

    var entry = el("div", { className: "activity-entry " + typeClass }, [
      el("span", { className: "activity-time", textContent: timestamp }),
      el("span", { className: "activity-msg", textContent: message })
    ]);

    // Prepend (newest at top)
    if (container.firstChild) {
      container.insertBefore(entry, container.firstChild);
    } else {
      container.appendChild(entry);
    }

    // Enforce limit
    var entries = $$(".activity-entry", container);
    while (entries.length > ACTIVITY_MAX_ENTRIES) {
      entries[entries.length - 1].remove();
      entries = $$(".activity-entry", container);
    }
  }

  function onStateTransition(data) {
    var badge = $("#workflow-state");
    var state = data.to || "IDLE";
    badge.textContent = state;
    badge.className = "state-badge " + getStateBadgeClass(state);

    // Show the panel when a transition happens
    $("#workflow-status").hidden = false;

    if (data.round != null) {
      $("#status-round").textContent = data.round;
    }

    var msg = "State: " + (data.from || "?") + " -> " + state;
    if (data.round != null) msg += " (round " + data.round + ")";
    addActivityEntry(msg, "info");
  }

  function onAgentDispatch(data) {
    addActivityEntry("Dispatching " + (data.agent || "?") + "...", "info");
  }

  function onAgentComplete(data) {
    if (data.success) {
      var msg = (data.agent || "?") + " completed";
      var details = [];
      if (data.duration_ms != null) details.push(data.duration_ms + "ms");
      if (data.cost_usd != null) details.push(formatCost(data.cost_usd));
      if (details.length > 0) msg += " (" + details.join(", ") + ")";
      addActivityEntry(msg, "success");
    } else {
      addActivityEntry((data.agent || "?") + " FAILED", "error");
    }
  }

  function onWorkflowStatus(data) {
    updateWorkflowStatus(data);
  }

  // -----------------------------------------------------------------------
  // Agent Telemetry Events (OTEL)
  // -----------------------------------------------------------------------

  function formatTokenCount(n) {
    if (n == null || isNaN(n)) return "0";
    if (n >= 1000000) return (n / 1000000).toFixed(1) + "M";
    if (n >= 1000) return (n / 1000).toFixed(1) + "K";
    return String(n);
  }

  function onAgentMetrics(data) {
    var panel = $("#agent-metrics");
    panel.hidden = false;

    $("#metric-tokens-in").textContent = formatTokenCount(data.input_tokens);
    $("#metric-tokens-out").textContent = formatTokenCount(data.output_tokens);
    $("#metric-tokens-cache").textContent = formatTokenCount(data.cache_read_tokens);
    $("#metric-api-calls").textContent = data.total_api_calls || 0;
    $("#metric-agent-cost").textContent = formatCost(data.total_cost_usd);
  }

  function onAgentToolEvent(data) {
    var status = data.success ? "success" : "error";
    var msg = "Tool: " + (data.tool_name || "?");
    if (data.duration_ms) msg += " (" + Math.round(data.duration_ms) + "ms)";
    if (!data.success) msg += " FAILED";
    addActivityEntry(msg, status);
  }

  function onAgentAPIEvent(data) {
    var msg = "API: " + (data.model || "?");
    var details = [];
    if (data.duration_ms) details.push(Math.round(data.duration_ms) + "ms");
    if (data.cost_usd) details.push(formatCost(data.cost_usd));
    if (details.length > 0) msg += " (" + details.join(", ") + ")";
    addActivityEntry(msg, "info");
  }

  // -----------------------------------------------------------------------
  // Persisted Metrics Restoration
  // -----------------------------------------------------------------------

  /**
   * Loads persisted OTEL metrics and events from SQLite via the HTTP API.
   * Called on page load and WebSocket reconnect to restore dashboard state
   * that would otherwise be lost on browser refresh.
   */
  function restorePersistedMetrics(featureName) {
    if (!featureName) return;
    fetchJSON("/api/metrics?feature=" + encodeURIComponent(featureName)).then(function (data) {
      if (!data) return;

      // Restore aggregate metrics panel (row 2)
      if (data.metrics) {
        onAgentMetrics({
          input_tokens: data.metrics.input_tokens || 0,
          output_tokens: data.metrics.output_tokens || 0,
          cache_read_tokens: data.metrics.cache_read_tokens || 0,
          total_api_calls: data.metrics.total_api_calls || 0,
          total_cost_usd: data.metrics.total_cost_usd || 0
        });
      }

      // Restore activity feed from persisted events (newest first from API,
      // but we add oldest first so newest ends up on top).
      if (data.events && data.events.length > 0) {
        var events = data.events.slice().reverse(); // oldest first
        for (var i = 0; i < events.length; i++) {
          var evt = events[i];
          if (evt.event_type === "tool") {
            var toolMsg = "Tool: " + (evt.tool_name || "?");
            if (evt.duration_ms) toolMsg += " (" + Math.round(evt.duration_ms) + "ms)";
            if (!evt.success) toolMsg += " FAILED";
            addActivityEntry(toolMsg, evt.success ? "success" : "error");
          } else if (evt.event_type === "api") {
            var apiMsg = "API: " + (evt.model || "?");
            var apiDetails = [];
            if (evt.duration_ms) apiDetails.push(Math.round(evt.duration_ms) + "ms");
            if (evt.cost_usd) apiDetails.push(formatCost(evt.cost_usd));
            if (apiDetails.length > 0) apiMsg += " (" + apiDetails.join(", ") + ")";
            addActivityEntry(apiMsg, "info");
          }
        }
      }
    }).catch(function (err) {
      console.warn("Failed to restore persisted metrics:", err);
    });
  }

  function pollWorkflowStatus() {
    fetchJSON("/api/workflow/status").then(function (data) {
      if (!data || !data.state) return;

      var state = data.state.toUpperCase();

      // Don't overwrite an active workflow display with an "idle" response.
      // This prevents the race where the poll returns idle before the
      // orchestrator goroutine has started.
      if (state === "IDLE" && workflowActive) return;

      if (state !== "IDLE") {
        updateWorkflowStatus(data);
      }

      // If idle or terminal, stop the workflow poller.
      if (state === "IDLE" || state === "FINALIZED" || state === "ESCALATED") {
        stopWorkflowPoller();
        return;
      }

      // If gate state and gate panel not already showing, show it.
      if (state.indexOf("HUMAN_GATE") !== -1) {
        var gatePanel = $(".gate-panel");
        if (!gatePanel) {
          var feature = data.feature_name;
          if (state === "HUMAN_GATE_1" && feature) {
            Promise.all([
              fetchJSON("/api/workspace/features/" + encodeURIComponent(feature) + "/discovery").catch(function () { return null; }),
              fetchJSON("/api/workspace/features/" + encodeURIComponent(feature) + "/files/gate1-corrections.json").catch(function () { return null; })
            ]).then(function (results) {
              if (results[0]) showGate1Panel({ gate_type: "requirements_confirmation", data: results[0], task_id: feature }, results[1]);
            });
          } else if (state === "HUMAN_GATE_2" && feature) {
            fetchJSON("/api/workspace/features/" + encodeURIComponent(feature) + "/files/drafter-output.json").then(function (drafter) {
              if (drafter) showGate2Panel({ gate_type: "ambiguity_resolution", data: drafter, task_id: feature });
            }).catch(function () {});
          }
        }
      }
    }).catch(function () {
      // No workflow running or endpoint not available — ignore
    });
  }

  function startWorkflowPoller() {
    if (workflowPoller) return;
    workflowPoller = setInterval(function () {
      pollWorkflowStatus();
    }, 5000);
  }

  function stopWorkflowPoller() {
    if (workflowPoller) {
      clearInterval(workflowPoller);
      workflowPoller = null;
    }
  }

  // -----------------------------------------------------------------------
  // HTTP helpers
  // -----------------------------------------------------------------------

  // -----------------------------------------------------------------------
  // Gate Form Persistence (localStorage)
  // -----------------------------------------------------------------------
  // Auto-saves gate form fields so data survives refresh, restart, or
  // failed submit. Keyed by feature name + gate ID.

  function gateStorageKey(feature, gateId) {
    return "gate-draft:" + feature + ":" + gateId;
  }

  function gateFormSave(feature, gateId, data) {
    if (!feature) return;
    try {
      localStorage.setItem(gateStorageKey(feature, gateId), JSON.stringify(data));
    } catch (e) { /* quota exceeded — silently ignore */ }
  }

  function gateFormLoad(feature, gateId) {
    if (!feature) return null;
    try {
      var raw = localStorage.getItem(gateStorageKey(feature, gateId));
      return raw ? JSON.parse(raw) : null;
    } catch (e) { return null; }
  }

  function gateFormClear(feature, gateId) {
    if (!feature) return;
    try {
      localStorage.removeItem(gateStorageKey(feature, gateId));
    } catch (e) { /* ignore */ }
  }

  /**
   * Collects all editable form state from a gate panel into a plain object.
   * Works for both Gate 1 and Gate 2.
   */
  function collectGate1FormState(panel) {
    var state = {};
    // Open question answers (by data-question-idx)
    var answers = {};
    $$(".gate-answer", panel).forEach(function (ta) {
      answers[ta.dataset.questionIdx] = ta.value;
    });
    state.answers = answers;
    // Assumption answers (by data-assumption-idx)
    var assumptions = {};
    $$(".gate-assumption-answer", panel).forEach(function (ta) {
      assumptions[ta.dataset.assumptionIdx] = ta.value;
    });
    state.assumptions = assumptions;
    // Editable fields (by data-field)
    var editables = {};
    $$(".gate-editable", panel).forEach(function (field) {
      if (field.dataset.field) {
        editables[field.dataset.field] = field.textContent;
      }
    });
    state.editables = editables;
    // Comment
    var commentEl = $("#gate1-comment");
    state.comment = commentEl ? commentEl.value : "";
    return state;
  }

  function restoreGate1FormState(panel, saved) {
    if (!saved) return;
    // Restore open question answers
    if (saved.answers) {
      $$(".gate-answer", panel).forEach(function (ta) {
        var val = saved.answers[ta.dataset.questionIdx];
        if (val) ta.value = val;
      });
    }
    // Restore assumption answers
    if (saved.assumptions) {
      $$(".gate-assumption-answer", panel).forEach(function (ta) {
        var val = saved.assumptions[ta.dataset.assumptionIdx];
        if (val) ta.value = val;
      });
    }
    // Restore editable fields
    if (saved.editables) {
      $$(".gate-editable", panel).forEach(function (field) {
        var val = saved.editables[field.dataset.field];
        if (val) field.textContent = val;
      });
    }
    // Restore comment
    if (saved.comment) {
      var commentEl = $("#gate1-comment");
      if (commentEl) commentEl.value = saved.comment;
    }
  }

  function collectGate2FormState(panel) {
    var state = {};
    // Action dropdowns (by data-idx)
    var actions = {};
    $$(".amb-action", panel).forEach(function (sel) {
      actions[sel.dataset.idx] = sel.value;
    });
    state.actions = actions;
    // Answer inputs (by data-idx)
    var answers = {};
    $$(".amb-answer", panel).forEach(function (input) {
      answers[input.dataset.idx] = input.value;
    });
    state.answers = answers;
    // Comment
    var commentEl = $("#gate2-comment");
    state.comment = commentEl ? commentEl.value : "";
    return state;
  }

  function restoreGate2FormState(panel, saved) {
    if (!saved) return;
    // Restore action dropdowns
    if (saved.actions) {
      $$(".amb-action", panel).forEach(function (sel) {
        var val = saved.actions[sel.dataset.idx];
        if (val) sel.value = val;
      });
    }
    // Restore answer inputs and enable/disable based on action
    if (saved.answers) {
      $$(".amb-answer", panel).forEach(function (input) {
        var val = saved.answers[input.dataset.idx];
        if (val) {
          input.value = val;
          // Enable the input if action is "answer"
          var sel = $(".amb-action[data-idx='" + input.dataset.idx + "']", panel);
          if (sel && sel.value === "answer") {
            input.disabled = false;
          }
        }
      });
    }
    // Restore comment
    if (saved.comment) {
      var commentEl = $("#gate2-comment");
      if (commentEl) commentEl.value = saved.comment;
    }
  }

  /**
   * Installs input/change listeners on all form elements within a gate panel
   * that auto-save to localStorage on every keystroke.
   */
  function installGateAutoSave(panel, feature, gateId, collectFn) {
    var save = function () {
      gateFormSave(feature, gateId, collectFn(panel));
    };
    // Debounce to avoid excessive writes
    var timer = null;
    var debouncedSave = function () {
      clearTimeout(timer);
      timer = setTimeout(save, 300);
    };
    panel.addEventListener("input", debouncedSave);
    panel.addEventListener("change", debouncedSave);
    // Also save on blur for contentEditable fields
    panel.addEventListener("blur", debouncedSave, true);
  }

  function fetchJSON(url, opts) {
    return fetch(url, opts).then(function (resp) {
      if (!resp.ok) {
        return resp.text().then(function (t) {
          throw new Error("HTTP " + resp.status + ": " + t);
        });
      }
      return resp.json();
    });
  }

  // -----------------------------------------------------------------------
  // WebSocket
  // -----------------------------------------------------------------------

  function wsConnect() {
    var protocol = location.protocol === "https:" ? "wss:" : "ws:";
    var url = protocol + "//" + location.host + "/ws";

    ws = new WebSocket(url);

    ws.onopen = function () {
      var wasReconnect = reconnectAttempt > 0;
      reconnectAttempt = 0;
      setWsStatus("connected");
      // Poll current workflow status to initialize panel if already running
      pollWorkflowStatus();
      // On reconnect, restore persisted metrics that may have been missed.
      if (wasReconnect) {
        fetchJSON("/api/workflow/status").then(function (status) {
          if (status && status.feature_name && status.state && status.state.toUpperCase() !== "IDLE") {
            restorePersistedMetrics(status.feature_name);
          }
        }).catch(function () {});
      }
    };

    ws.onclose = function () {
      ws = null;
      setWsStatus("disconnected");
      scheduleReconnect();
    };

    ws.onerror = function () {
      setWsStatus("disconnected");
    };

    ws.onmessage = function (evt) {
      var envelope;
      try { envelope = JSON.parse(evt.data); } catch (e) { return; }
      handleEvent(envelope);
    };
  }

  function scheduleReconnect() {
    if (reconnectTimer) return;
    setWsStatus("reconnecting");
    var delay = Math.min(RECONNECT_BASE_MS * Math.pow(2, reconnectAttempt), RECONNECT_MAX_MS);
    reconnectAttempt++;
    reconnectTimer = setTimeout(function () {
      reconnectTimer = null;
      wsConnect();
    }, delay);
  }

  function setWsStatus(state) {
    var indicator = $("#ws-indicator");
    indicator.className = "ws-indicator ws-" + state;
    indicator.title = "WebSocket " + state;
  }

  // -----------------------------------------------------------------------
  // Event Dispatch
  // -----------------------------------------------------------------------

  function handleEvent(envelope) {
    // Keepalive ping — ignore silently.
    if (envelope.event === "ping") return;

    // Add ALL WebSocket events to the Messages tab log.
    addWsEventToMessages(envelope);

    switch (envelope.event) {
      case "spec_version":
        onSpecVersion(envelope.data);
        break;
      case "issue_update":
        onIssueUpdate(envelope.data);
        break;
      case "convergence_update":
        onConvergenceUpdate(envelope.data);
        break;
      case "gate_request":
        onGateRequest(envelope.data);
        break;
      case "gate_response":
        onGateResponse(envelope.data);
        break;
      case "circuit_breaker":
        onCircuitBreaker(envelope.data);
        break;
      case "agent_error":
        onAgentError(envelope.data);
        break;
      case "state_transition":
        onStateTransition(envelope.data);
        break;
      case "agent_dispatch":
        onAgentDispatch(envelope.data);
        break;
      case "agent_complete":
        onAgentComplete(envelope.data);
        break;
      case "workflow_status":
        onWorkflowStatus(envelope.data);
        break;
      case "agent_metrics":
        onAgentMetrics(envelope.data);
        break;
      case "agent_tool_event":
        onAgentToolEvent(envelope.data);
        break;
      case "agent_api_event":
        onAgentAPIEvent(envelope.data);
        break;
    }
  }

  // -----------------------------------------------------------------------
  // Tab Navigation
  // -----------------------------------------------------------------------

  function initTabs() {
    $$(".nav-tab").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var tab = btn.dataset.tab;
        $$(".nav-tab").forEach(function (b) { b.classList.remove("active"); });
        $$(".tab-panel").forEach(function (p) { p.classList.remove("active"); });
        btn.classList.add("active");
        $("#tab-" + tab).classList.add("active");

        // Lazy-load data on tab switch
        if (tab === "controls") { loadFeatureList(); startFeatureListPolling(); }
        if (tab === "spec") loadSpec();
        if (tab === "issues") loadIssues();
        if (tab === "convergence") loadConvergence();
        if (tab === "messages") startMessagesPolling();

        // Stop polling when leaving tabs
        if (tab !== "messages") stopMessagesPolling();
        if (tab !== "controls") stopFeatureListPolling();
      });
    });
  }

  // -----------------------------------------------------------------------
  // Controls Tab — Workspace Browser (Feature List)
  // -----------------------------------------------------------------------

  var featureListTimer = null;

  function getWorkflowStateBadgeClass(state) {
    var s = (state || "").toUpperCase();
    if (s === "FINALIZED") return "ws-finalized";
    if (s === "ESCALATED" || s === "ERROR") return "ws-escalated";
    if (s.indexOf("HUMAN_GATE") !== -1) return "ws-gate";
    if (s === "UNKNOWN") return "ws-unknown";
    // Active states: INIT, DISCOVERY, DRAFTING, REVIEWING, REVISING, JUDGING
    return "ws-active";
  }

  function loadFeatureList() {
    fetchJSON("/api/workspace/features").then(function (features) {
      var container = $("#workflow-list");
      if (!container) return;
      clearChildren(container);

      if (!features || features.length === 0) {
        container.appendChild(el("p", { className: "workflow-empty", textContent: "No workflows yet. Start one above." }));
        return;
      }

      features.forEach(function (f) {
        var card = el("div", { className: "workflow-card" });

        // Info section
        var info = el("div", { className: "workflow-info" });

        var nameRow = el("div", { className: "workflow-name" });
        nameRow.appendChild(document.createTextNode(f.feature_name + " "));
        var badge = el("span", {
          className: "workflow-state-badge " + getWorkflowStateBadgeClass(f.state),
          textContent: f.state
        });
        nameRow.appendChild(badge);
        info.appendChild(nameRow);

        var meta = el("div", { className: "workflow-meta" });
        if (f.round > 0) {
          meta.appendChild(el("span", { textContent: "Round " + f.round }));
        }
        if (f.cost_usd > 0) {
          meta.appendChild(el("span", { textContent: "Cost " + formatCost(f.cost_usd) }));
        }
        if (f.started_at) {
          meta.appendChild(el("span", { textContent: "Started: " + formatDate(f.started_at) }));
        }
        if (f.updated_at) {
          meta.appendChild(el("span", { textContent: "Updated: " + formatDate(f.updated_at) }));
        }
        if (f.files) {
          meta.appendChild(el("span", { textContent: f.files.length + " files" }));
        }
        if (f.spec_versions > 0) {
          meta.appendChild(el("span", { textContent: f.spec_versions + " spec versions" }));
        }
        info.appendChild(meta);
        card.appendChild(info);

        // Actions section
        var actions = el("div", { className: "workflow-actions" });

        var stateUpper = (f.state || "").toUpperCase();
        var isTerminal = f.is_terminal;
        var isPaused = f.is_paused;
        var isGate = stateUpper.indexOf("HUMAN_GATE") !== -1;
        var isActive = !isTerminal && !isGate && !isPaused && stateUpper !== "UNKNOWN";

        if ((isTerminal || isPaused) && stateUpper !== "UNKNOWN") {
          // Resume button — continues from where it left off
          if (stateUpper === "ESCALATED" || stateUpper === "ERROR" || isPaused) {
            var termResumeBtn = el("button", {
              className: "btn btn-sm",
              textContent: "Resume",
              style: "background:#d4edda;color:#155724;border-color:#c3e6cb;"
            });
            termResumeBtn.addEventListener("click", (function (featureName) {
              return function () {
                if (!confirm("Resume workflow \"" + featureName + "\" from where it left off? The wall clock timer will be reset.")) return;
                termResumeBtn.disabled = true;
                termResumeBtn.textContent = "Resuming...";
                fetchJSON("/api/workflow/resume", {
                  method: "POST",
                  headers: { "Content-Type": "application/json" },
                  body: JSON.stringify({ feature_name: featureName })
                }).then(function (data) {
                  updateWorkflowStatus({
                    state: data.resume_state || "REVIEWING",
                    feature_name: featureName,
                    round: 1,
                    cost_usd: 0,
                    wall_clock_seconds: 0,
                    agent_invocations: 0
                  });
                  addActivityEntry("Workflow resumed: " + featureName + " from " + (data.resume_state || "?"), "info");
                  startWorkflowPoller();
                  loadFeatureList();
                }).catch(function (err) {
                  alert("Resume failed: " + err.message);
                  termResumeBtn.disabled = false;
                  termResumeBtn.textContent = "Resume";
                });
              };
            })(f.feature_name));
            actions.appendChild(termResumeBtn);
          }

          // Restart button — resets and pre-fills form
          var restartBtn = el("button", {
            className: "btn btn-primary btn-sm",
            textContent: "Restart"
          });
          restartBtn.addEventListener("click", (function (featureName) {
            return function () {
              if (!confirm("Restart workflow for \"" + featureName + "\"? This will delete all previous results and start fresh.")) return;
              restartBtn.disabled = true;
              restartBtn.textContent = "Restarting...";
              fetchJSON("/api/workflow/reset", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ feature_name: featureName })
              }).then(function () {
                // Auto-start the workflow with the same feature name.
                return fetchJSON("/api/workflow/start", {
                  method: "POST",
                  headers: { "Content-Type": "application/json" },
                  body: JSON.stringify({
                    title: featureName,
                    feature_name: featureName,
                    description: "Restarted workflow"
                  })
                });
              }).then(function (data) {
                updateWorkflowStatus({
                  state: data.state || "INIT",
                  feature_name: data.feature_name || featureName,
                  round: data.round || 1,
                  cost_usd: 0,
                  wall_clock_seconds: 0,
                  agent_invocations: 0
                });
                addActivityEntry("Workflow restarted: " + featureName, "info");
                startWorkflowPoller();
                loadFeatureList();
              }).catch(function (err) {
                alert("Restart failed: " + err.message);
                restartBtn.disabled = false;
                restartBtn.textContent = "Restart";
              });
            };
          })(f.feature_name));
          actions.appendChild(restartBtn);
        }

        if (isGate) {
          // Resume button for gate states — starts orchestrator and shows gate panel
          var resumeBtn = el("button", {
            className: "btn btn-sm",
            textContent: "Resume",
            style: "background:#e8d5f5;color:#6f42c1;border-color:#d5b8eb;"
          });
          resumeBtn.addEventListener("click", (function (featureName, stateStr) {
            return function () {
              resumeBtn.disabled = true;
              resumeBtn.textContent = "Resuming...";

              // Send a dummy confirm to trigger auto-resume of the orchestrator,
              // then immediately cancel the gate response so the orchestrator
              // re-enters the gate wait. Actually — better approach: just start
              // a new workflow which will auto-restore from disk state.
              fetchJSON("/api/workflow/start", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({
                  title: featureName,
                  feature_name: featureName,
                  description: "Resumed from " + stateStr
                })
              }).then(function (data) {
                // Orchestrator is now running and waiting at the gate.
                updateWorkflowStatus({
                  state: data.state || stateStr,
                  feature_name: data.feature_name || featureName,
                  round: data.round || 1,
                  cost_usd: 0,
                  wall_clock_seconds: 0,
                  agent_invocations: 0
                });
                addActivityEntry("Workflow resumed: " + featureName + " at " + stateStr, "info");
                startWorkflowPoller();
                loadFeatureList();

                // Fetch the gate data (and previous corrections) and show the gate panel.
                if (stateStr.indexOf("HUMAN_GATE_1") !== -1) {
                  Promise.all([
                    fetchJSON("/api/workspace/features/" + encodeURIComponent(featureName) + "/discovery").catch(function () { return null; }),
                    fetchJSON("/api/workspace/features/" + encodeURIComponent(featureName) + "/files/gate1-corrections.json").catch(function () { return null; })
                  ]).then(function (results) {
                    if (results[0]) showGate1Panel({ gate_type: "requirements_confirmation", data: results[0], task_id: featureName }, results[1]);
                  });
                } else if (stateStr.indexOf("HUMAN_GATE_2") !== -1) {
                  fetchJSON("/api/workspace/features/" + encodeURIComponent(featureName) + "/files/drafter-output.json").then(function (drafter) {
                    showGate2Panel({ gate_type: "ambiguity_resolution", data: drafter, task_id: featureName });
                  });
                }
              }).catch(function (err) {
                // If 409, orchestrator may already be running — just show the gate panel.
                if (String(err.message).indexOf("409") !== -1) {
                  if (stateStr.indexOf("HUMAN_GATE_1") !== -1) {
                    Promise.all([
                      fetchJSON("/api/workspace/features/" + encodeURIComponent(featureName) + "/discovery").catch(function () { return null; }),
                      fetchJSON("/api/workspace/features/" + encodeURIComponent(featureName) + "/files/gate1-corrections.json").catch(function () { return null; })
                    ]).then(function (results) {
                      if (results[0]) showGate1Panel({ gate_type: "requirements_confirmation", data: results[0], task_id: featureName }, results[1]);
                    });
                  }
                } else {
                  alert("Resume failed: " + err.message);
                }
                resumeBtn.disabled = false;
                resumeBtn.textContent = "Resume";
              });
            };
          })(f.feature_name, f.state));
          actions.appendChild(resumeBtn);
        }

        if (isActive) {
          // View button — switches to messages tab
          var viewBtn = el("button", {
            className: "btn btn-sm",
            textContent: "View"
          });
          viewBtn.addEventListener("click", function () {
            // Switch to messages tab
            $$(".nav-tab").forEach(function (b) { b.classList.remove("active"); });
            $$(".tab-panel").forEach(function (p) { p.classList.remove("active"); });
            var msgTab = $(".nav-tab[data-tab='messages']");
            if (msgTab) msgTab.classList.add("active");
            $("#tab-messages").classList.add("active");
            startMessagesPolling();
          });
          actions.appendChild(viewBtn);
        }

        // Restart button for active or gate states — stops and starts fresh.
        if (isActive || isGate) {
          var liveRestartBtn = el("button", {
            className: "btn btn-sm",
            textContent: "Restart",
            style: "background:#fff3cd;color:#856404;border-color:#ffc107;"
          });
          liveRestartBtn.addEventListener("click", (function (featureName) {
            return function () {
              if (!confirm("Stop and restart workflow for \"" + featureName + "\"? This will cancel the running workflow, delete all results, and start fresh.")) return;
              liveRestartBtn.disabled = true;
              liveRestartBtn.textContent = "Restarting...";
              fetchJSON("/api/workflow/restart", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ feature_name: featureName })
              }).then(function () {
                // Auto-start fresh workflow.
                return fetchJSON("/api/workflow/start", {
                  method: "POST",
                  headers: { "Content-Type": "application/json" },
                  body: JSON.stringify({
                    title: featureName,
                    feature_name: featureName,
                    description: "Restarted workflow"
                  })
                });
              }).then(function (data) {
                updateWorkflowStatus({
                  state: data.state || "INIT",
                  feature_name: data.feature_name || featureName,
                  round: data.round || 1,
                  cost_usd: 0,
                  wall_clock_seconds: 0,
                  agent_invocations: 0
                });
                addActivityEntry("Workflow restarted: " + featureName, "info");
                clearChildren($("#gate-panels"));
                startWorkflowPoller();
                loadFeatureList();
              }).catch(function (err) {
                alert("Restart failed: " + err.message);
                liveRestartBtn.disabled = false;
                liveRestartBtn.textContent = "Restart";
              });
            };
          })(f.feature_name));
          actions.appendChild(liveRestartBtn);
        }

        // Delete button (always shown for features with content)
        var deleteBtn = el("button", {
          className: "btn btn-danger btn-sm",
          textContent: "Delete"
        });
        deleteBtn.addEventListener("click", (function (featureName) {
          return function () {
            if (!confirm("Delete all files for '" + featureName + "'? This cannot be undone.")) return;
            fetchJSON("/api/workflow/reset", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ feature_name: featureName })
            }).then(function () {
              loadFeatureList();
            }).catch(function (err) {
              alert("Delete failed: " + err.message);
            });
          };
        })(f.feature_name));
        actions.appendChild(deleteBtn);

        card.appendChild(actions);
        container.appendChild(card);
      });
    }).catch(function () {
      var container = $("#workflow-list");
      if (container) {
        clearChildren(container);
        container.appendChild(el("p", { className: "workflow-empty", textContent: "Failed to load workflows." }));
      }
    });
  }

  function startFeatureListPolling() {
    if (featureListTimer) clearInterval(featureListTimer);
    featureListTimer = setInterval(function () {
      // Only auto-refresh while Controls tab is active
      var controlsTab = $("#tab-controls");
      if (controlsTab && controlsTab.classList.contains("active")) {
        loadFeatureList();
      }
    }, 10000);
  }

  function stopFeatureListPolling() {
    if (featureListTimer) {
      clearInterval(featureListTimer);
      featureListTimer = null;
    }
  }

  // -----------------------------------------------------------------------
  // Controls Tab — Goal Form
  // -----------------------------------------------------------------------

  function initGoalForm() {
    var form = $("#goal-form");
    form.addEventListener("submit", function (e) {
      e.preventDefault();
      var payload = {
        title: $("#goal-title").value.trim(),
        feature_name: $("#goal-feature-name").value.trim(),
        description: $("#goal-description").value.trim()
      };

      var submitBtn = $("#goal-submit");
      submitBtn.disabled = true;
      submitBtn.textContent = "Starting...";

      fetchJSON("/api/workflow/start", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      }).then(function (data) {
        updateWorkflowStatus({
          state: data.state || "INIT",
          feature_name: data.feature_name || payload.feature_name,
          round: data.round || 1,
          cost_usd: 0,
          wall_clock_seconds: 0,
          agent_invocations: 0
        });
        addActivityEntry("Workflow started: " + (data.feature_name || payload.feature_name), "info");
        startWorkflowPoller();
        form.reset();
        // Collapse the new workflow section and refresh the list
        var details = $("#new-workflow-section");
        if (details) details.open = false;
        loadFeatureList();
      }).catch(function (err) {
        var msg = err.message || "";
        if (msg.indexOf("409") !== -1) {
          alert("A workflow is already in progress for this feature. Delete the workspace/specs/" + payload.feature_name + "/workflow-state.json file to start fresh, or wait for the current workflow to finish.");
        } else {
          alert("Failed to start workflow: " + msg);
        }
      }).finally(function () {
        submitBtn.disabled = false;
        submitBtn.textContent = "Submit Goal";
      });
    });
  }

  // -----------------------------------------------------------------------
  // Controls Tab — File Upload
  // -----------------------------------------------------------------------

  function initUpload() {
    var zone = $("#upload-zone");
    var input = $("#upload-input");

    zone.addEventListener("click", function () { input.click(); });
    zone.addEventListener("keydown", function (e) {
      if (e.key === "Enter" || e.key === " ") { e.preventDefault(); input.click(); }
    });

    input.addEventListener("change", function () {
      if (input.files.length > 0) uploadFiles(input.files);
      input.value = "";
    });

    // Drag and drop
    zone.addEventListener("dragover", function (e) {
      e.preventDefault();
      zone.classList.add("drag-over");
    });

    zone.addEventListener("dragleave", function () {
      zone.classList.remove("drag-over");
    });

    zone.addEventListener("drop", function (e) {
      e.preventDefault();
      zone.classList.remove("drag-over");
      if (e.dataTransfer.files.length > 0) uploadFiles(e.dataTransfer.files);
    });

    // Load existing uploads
    refreshUploadList();
  }

  function uploadFiles(fileList) {
    var progressContainer = $("#upload-progress");

    Array.from(fileList).forEach(function (file) {
      var ext = "." + file.name.split(".").pop().toLowerCase();
      if (ALLOWED_EXTENSIONS.indexOf(ext) === -1) {
        showUploadError(progressContainer, file.name, "Type not allowed: " + ext);
        return;
      }

      var row = el("div", { className: "upload-progress-item" }, [
        el("span", { className: "filename", textContent: file.name }),
        el("div", { className: "upload-progress-bar" }, [
          el("div", { className: "upload-progress-fill", style: "width: 0%" })
        ]),
        el("span", { className: "status-text", textContent: "0%" })
      ]);
      progressContainer.appendChild(row);

      var fillBar = $(".upload-progress-fill", row);
      var statusText = $(".status-text", row);

      var formData = new FormData();
      formData.append("file", file);

      var xhr = new XMLHttpRequest();
      xhr.open("POST", "/api/workspace/upload", true);

      xhr.upload.addEventListener("progress", function (pe) {
        if (pe.lengthComputable) {
          var pct = Math.round((pe.loaded / pe.total) * 100);
          fillBar.style.width = pct + "%";
          statusText.textContent = pct + "%";
        }
      });

      xhr.addEventListener("load", function () {
        if (xhr.status === 201) {
          fillBar.style.width = "100%";
          fillBar.classList.add("success");
          statusText.textContent = "Done";
          refreshUploadList();
          // Remove progress row after 3s
          setTimeout(function () { row.remove(); }, 3000);
        } else {
          fillBar.classList.add("error");
          statusText.textContent = "Error";
        }
      });

      xhr.addEventListener("error", function () {
        fillBar.classList.add("error");
        statusText.textContent = "Error";
      });

      xhr.send(formData);
    });
  }

  function showUploadError(container, name, msg) {
    var row = el("div", { className: "upload-progress-item" }, [
      el("span", { className: "filename", textContent: name }),
      el("div", { className: "upload-progress-bar" }, [
        el("div", { className: "upload-progress-fill error", style: "width: 100%" })
      ]),
      el("span", { className: "status-text", textContent: msg })
    ]);
    container.appendChild(row);
    setTimeout(function () { row.remove(); }, 5000);
  }

  function refreshUploadList() {
    fetchJSON("/api/workspace/uploads").then(function (files) {
      var tbody = $("#upload-file-list tbody");
      clearChildren(tbody);
      (files || []).forEach(function (f) {
        var tr = el("tr", null, [
          el("td", { textContent: f.name }),
          el("td", { textContent: formatBytes(f.size) }),
          el("td", { textContent: formatDate(f.modified_at) }),
          el("td", null, [
            el("button", {
              className: "btn btn-sm btn-danger",
              textContent: "Remove",
              onClick: function () {
                fetchJSON("/api/workspace/upload/" + encodeURIComponent(f.name), { method: "DELETE" })
                  .then(refreshUploadList)
                  .catch(function () {});
              }
            })
          ])
        ]);
        tbody.appendChild(tr);
      });
    }).catch(function () {});
  }

  // -----------------------------------------------------------------------
  // Spec Tab
  // -----------------------------------------------------------------------

  function loadSpec() {
    loadSpecVersions();
    loadCurrentSpec();
  }

  function loadSpecVersions() {
    fetchJSON("/api/spec/versions").then(function (versions) {
      var select = $("#spec-version-select");
      var current = select.value;
      clearChildren(select);
      select.appendChild(el("option", { value: "", textContent: "Current" }));
      (versions || []).forEach(function (v) {
        var opt = el("option", { value: String(v.version), textContent: "v" + v.version + " (" + v.modified_at + ")" });
        select.appendChild(opt);
      });
      if (current) select.value = current;
      $("#btn-diff").disabled = !versions || versions.length < 2;
    }).catch(function () {});
  }

  function loadCurrentSpec() {
    var select = $("#spec-version-select");
    var version = select.value;
    var url = version ? "/api/spec/version/" + version : "/api/spec/current";

    fetchJSON(url).then(function (data) {
      $("#spec-content").innerHTML = renderMarkdown(data.content || "");
      // Hide diff view when loading spec
      $("#spec-diff-view").hidden = true;
    }).catch(function () {
      $("#spec-content").innerHTML = "<p><em>No spec available yet.</em></p>";
    });
  }

  function initSpecControls() {
    $("#spec-version-select").addEventListener("change", loadCurrentSpec);

    $("#btn-diff").addEventListener("click", function () {
      var select = $("#spec-version-select");
      var selected = parseInt(select.value, 10);
      // Collect all available versions from the dropdown.
      var opts = $$("option", select).map(function (o) { return parseInt(o.value, 10); }).filter(function (n) { return !isNaN(n) && n >= 0; });
      opts.sort(function (a, b) { return a - b; });
      if (opts.length < 2) return;

      if (isNaN(selected) || selected <= opts[0]) {
        // No valid selection or selected is the lowest — diff the latest two.
        showDiff(opts[opts.length - 2], opts[opts.length - 1]);
      } else {
        // Diff selected version against its predecessor in the list.
        var idx = opts.indexOf(selected);
        if (idx > 0) {
          showDiff(opts[idx - 1], selected);
        } else {
          showDiff(opts[opts.length - 2], opts[opts.length - 1]);
        }
      }
    });
  }

  function showDiff(a, b) {
    fetchJSON("/api/spec/diff/" + a + "/" + b).then(function (data) {
      var diffView = $("#spec-diff-view");
      diffView.hidden = false;

      var content = "";
      content += '<div class="diff-header"><span>Diff: v' + a + ' → v' + b + '</span>';
      content += '<button class="btn btn-sm" id="btn-close-diff">Close</button></div>';

      var lines = (data.diff || "").split("\n");
      for (var i = 0; i < lines.length; i++) {
        var line = lines[i];
        var cls = "diff-line diff-line-ctx";
        if (line.startsWith("+ ")) cls = "diff-line diff-line-add";
        else if (line.startsWith("- ")) cls = "diff-line diff-line-del";
        content += '<div class="' + cls + '">' + escapeHtml(line) + "</div>";
      }

      diffView.innerHTML = content;

      $("#btn-close-diff").addEventListener("click", function () {
        diffView.hidden = true;
      });
    }).catch(function (err) {
      alert("Failed to load diff: " + err.message);
    });
  }

  function onSpecVersion(data) {
    // Reload spec and version list
    loadSpecVersions();
    loadCurrentSpec();
  }

  // -----------------------------------------------------------------------
  // Issues Tab
  // -----------------------------------------------------------------------

  function loadIssues() {
    var params = new URLSearchParams();
    var sev = $("#filter-severity").value;
    var status = $("#filter-status").value;
    var lens = $("#filter-lens").value;
    if (sev) params.set("severity", sev);
    if (status) params.set("status", status);
    if (lens) params.set("lens", lens);

    var url = "/api/spec/issues" + (params.toString() ? "?" + params.toString() : "");
    fetchJSON(url).then(function (issues) {
      issueCache = issues || [];
      renderIssues(issueCache);
      updateIssueSummary(issueCache);
      updateLensFilter(issueCache);
    }).catch(function () {});
  }

  function renderIssues(issues) {
    var tbody = $("#issue-table tbody");
    clearChildren(tbody);

    issues.forEach(function (issue) {
      var f = issue.finding;
      var tr = el("tr", { className: "expandable" });
      tr.innerHTML =
        "<td>" + escapeHtml(f.id) + "</td>" +
        "<td>" + severityBadge(f.severity) + "</td>" +
        "<td>" + escapeHtml(f.lens || "-") + "</td>" +
        "<td>" + escapeHtml(f.affected_section || "-") + "</td>" +
        "<td>" + statusBadge(issue.status) + "</td>" +
        "<td>" + (f.round_raised || "-") + "</td>" +
        "<td>" + (f.round_closed != null ? f.round_closed : "-") + "</td>";

      tr.addEventListener("click", function () {
        var next = tr.nextElementSibling;
        if (next && next.classList.contains("issue-detail")) {
          next.remove();
        } else {
          var detail = buildIssueDetail(issue);
          tr.parentNode.insertBefore(detail, tr.nextSibling);
        }
      });

      tbody.appendChild(tr);
    });
  }

  function buildIssueDetail(issue) {
    var f = issue.finding;
    var tr = el("tr", { className: "issue-detail" });
    var td = el("td", { colSpan: "7" });

    var html = '<div class="issue-detail-content">';
    html += '<div><dt>Description</dt><dd>' + escapeHtml(f.description || "-") + "</dd></div>";
    html += '<div><dt>Impact</dt><dd>' + escapeHtml(f.impact || "-") + "</dd></div>";
    html += '<div><dt>Recommendation</dt><dd>' + escapeHtml(f.recommendation || "-") + "</dd></div>";
    html += '<div><dt>Resolution Notes</dt><dd>' + escapeHtml(f.resolution_notes || "-") + "</dd></div>";

    if (f.source_ids && f.source_ids.length > 0) {
      html += '<div><dt>Source IDs</dt><dd>' + f.source_ids.map(escapeHtml).join(", ") + "</dd></div>";
    }
    if (f.raised_by && f.raised_by.length > 0) {
      html += '<div><dt>Raised By</dt><dd>' + f.raised_by.map(escapeHtml).join(", ") + "</dd></div>";
    }

    // History
    if (issue.status_history && issue.status_history.length > 0) {
      html += '<div class="issue-history"><dt>History</dt><dd>';
      html += '<table class="table"><thead><tr><th>From</th><th>To</th><th>Round</th><th>Reason</th><th>Time</th></tr></thead><tbody>';
      issue.status_history.forEach(function (ch) {
        html += "<tr>" +
          "<td>" + statusBadge(ch.from) + "</td>" +
          "<td>" + statusBadge(ch.to) + "</td>" +
          "<td>" + ch.round + "</td>" +
          "<td>" + escapeHtml(ch.reason || "-") + "</td>" +
          "<td>" + formatDate(ch.timestamp) + "</td>" +
          "</tr>";
      });
      html += "</tbody></table></dd></div>";
    }

    html += "</div>";
    td.innerHTML = html;
    tr.appendChild(td);
    return tr;
  }

  function updateIssueSummary(issues) {
    var openCritical = 0, openMajor = 0, totalRaised = 0, totalClosed = 0;
    var terminal = { closed: true, dismissed: true, acknowledged: true };

    issues.forEach(function (issue) {
      totalRaised++;
      if (issue.status === "closed") totalClosed++;
      if (!terminal[issue.status]) {
        if (issue.finding.severity === "CRITICAL") openCritical++;
        if (issue.finding.severity === "MAJOR") openMajor++;
      }
    });

    $("#stat-open-critical").textContent = openCritical;
    $("#stat-open-major").textContent = openMajor;
    $("#stat-total-raised").textContent = totalRaised;
    $("#stat-total-closed").textContent = totalClosed;
  }

  function updateLensFilter(issues) {
    issues.forEach(function (issue) {
      if (issue.finding.lens) lensSet.add(issue.finding.lens);
    });
    var select = $("#filter-lens");
    var current = select.value;
    clearChildren(select);
    select.appendChild(el("option", { value: "", textContent: "All" }));
    Array.from(lensSet).sort().forEach(function (lens) {
      select.appendChild(el("option", { value: lens, textContent: lens }));
    });
    if (current) select.value = current;
  }

  function initIssueFilters() {
    ["#filter-severity", "#filter-status", "#filter-lens"].forEach(function (sel) {
      $(sel).addEventListener("change", loadIssues);
    });
  }

  function onIssueUpdate(data) {
    // Refresh issues when updates arrive
    loadIssues();
  }

  // -----------------------------------------------------------------------
  // Convergence Tab
  // -----------------------------------------------------------------------

  function loadConvergence() {
    fetchJSON("/api/spec/convergence").then(function (data) {
      renderConvergence(data);
    }).catch(function () {
      $("#conv-round").textContent = "-";
      $("#conv-verdict").innerHTML = "-";
      $("#conv-progress").textContent = "-";
      $("#conv-rationale").textContent = "No convergence data yet.";
    });
  }

  function renderConvergence(data) {
    $("#conv-round").textContent = data.round || "-";
    $("#conv-verdict").innerHTML = data.latest_verdict ? verdictBadge(data.latest_verdict) : "-";

    var pct = data.progress != null ? Math.round(data.progress * 100) + "%" : "-";
    $("#conv-progress").textContent = pct;

    $("#conv-open-critical").textContent = data.open_critical || 0;
    $("#conv-open-major").textContent = data.open_major || 0;
    $("#conv-open-minor").textContent = data.open_minor || 0;
  }

  function onConvergenceUpdate(data) {
    // Update inline metrics
    $("#conv-round").textContent = data.round || "-";
    $("#conv-verdict").innerHTML = data.verdict ? verdictBadge(data.verdict) : "-";
    $("#conv-open-critical").textContent = data.open_critical || 0;
    $("#conv-open-major").textContent = data.open_major || 0;
    $("#conv-open-minor").textContent = data.open_minor || 0;
    $("#conv-rationale").textContent = data.rationale || "";

    // Add to history
    convergenceHistory.push(data);
    renderConvergenceHistory();

    // Also refresh full convergence data
    loadConvergence();
  }

  function renderConvergenceHistory() {
    var tbody = $("#conv-history-body");
    clearChildren(tbody);

    for (var i = 0; i < convergenceHistory.length; i++) {
      var h = convergenceHistory[i];
      var prev = i > 0 ? convergenceHistory[i - 1] : null;

      var totalIssues = (h.open_critical || 0) + (h.open_major || 0) + (h.open_minor || 0);
      var prevTotal = prev ? ((prev.open_critical || 0) + (prev.open_major || 0) + (prev.open_minor || 0)) : totalIssues;

      var trendClass = "trend-stable";
      if (totalIssues < prevTotal) trendClass = "trend-improving";
      else if (totalIssues > prevTotal) trendClass = "trend-worsening";

      var tr = el("tr");
      tr.innerHTML =
        "<td>" + h.round + "</td>" +
        "<td>" + verdictBadge(h.verdict) + "</td>" +
        "<td>" + (h.open_critical || 0) + "</td>" +
        "<td>" + (h.open_major || 0) + "</td>" +
        "<td>" + (h.open_minor || 0) + "</td>" +
        '<td><span class="' + trendClass + '">' + totalIssues + " open</span></td>";
      tbody.appendChild(tr);
    }
  }

  // -----------------------------------------------------------------------
  // Human Gates
  // -----------------------------------------------------------------------

  function onGateRequest(data) {
    if (data.gate_type === "requirements_confirmation") {
      showGate1Panel(data);
    } else if (data.gate_type === "ambiguity_resolution") {
      showGate2Panel(data);
    }
  }

  function onGateResponse(data) {
    addActivityEntry(
      "Gate response: " + (data.action || "unknown") + " — " + (data.detail || ""),
      data.action === "cancel" ? "error" : "success"
    );
    // Clear the gate panel since the response was accepted.
    var container = $("#gate-panels");
    if (container) clearChildren(container);
  }

  // --- Gate 1: Requirements Confirmation ---

  function showGate1Panel(data, previousCorrections) {
    var container = $("#gate-panels");
    clearChildren(container);

    var discovery = data.data || {};
    var taskId = data.task_id || "";

    var panel = el("div", { className: "gate-panel" });
    var header = '<div style="display:flex;justify-content:space-between;align-items:center;">' +
      '<h3><span class="gate-badge">Human Gate 1</span> Requirements Confirmation</h3>' +
      '<button id="gate1-copy" class="btn btn-sm" style="background:#eee;color:#333;">Copy</button>' +
      '</div>';
    var content = "";

    // Problem statement
    content += buildGateSection("Problem Statement", discovery.problem_statement, true);

    // Actors
    if (discovery.actors && discovery.actors.length > 0) {
      var actorsHtml = "<ul>";
      discovery.actors.forEach(function (a) {
        actorsHtml += "<li><strong>" + escapeHtml(a.name) + "</strong> (" + escapeHtml(a.type) + "): " + escapeHtml(a.description) + "</li>";
      });
      actorsHtml += "</ul>";
      content += buildGateSectionHtml("Actors", actorsHtml);
    }

    // Scope
    if (discovery.scope) {
      var scopeHtml = "<strong>In scope:</strong><ul>";
      (discovery.scope.in_scope || []).forEach(function (s) { scopeHtml += "<li>" + escapeHtml(s) + "</li>"; });
      scopeHtml += "</ul>";
      if (discovery.scope.out_of_scope && discovery.scope.out_of_scope.length > 0) {
        scopeHtml += "<strong>Out of scope:</strong><ul>";
        discovery.scope.out_of_scope.forEach(function (s) { scopeHtml += "<li>" + escapeHtml(s) + "</li>"; });
        scopeHtml += "</ul>";
      }
      content += buildGateSectionHtml("Scope", scopeHtml);
    }

    // Constraints
    if (discovery.constraints && discovery.constraints.length > 0) {
      var cHtml = "<ul>";
      discovery.constraints.forEach(function (c) { cHtml += "<li>" + escapeHtml(c) + "</li>"; });
      cHtml += "</ul>";
      content += buildGateSectionHtml("Constraints", cHtml);
    }

    // Integration points
    if (discovery.integration_points && discovery.integration_points.length > 0) {
      var ipHtml = "<ul>";
      discovery.integration_points.forEach(function (ip) {
        ipHtml += "<li><strong>" + escapeHtml(ip.system) + "</strong> (" + escapeHtml(ip.direction) + "): " + escapeHtml(ip.description) + "</li>";
      });
      ipHtml += "</ul>";
      content += buildGateSectionHtml("Integration Points", ipHtml);
    }

    // Priorities
    if (discovery.priorities && discovery.priorities.length > 0) {
      var prHtml = "<ul>";
      discovery.priorities.forEach(function (p) {
        prHtml += "<li><strong>" + escapeHtml(p.priority) + "</strong>: " + escapeHtml(p.item) + " - " + escapeHtml(p.rationale) + "</li>";
      });
      prHtml += "</ul>";
      content += buildGateSectionHtml("Priorities", prHtml);
    }

    // Assumptions
    if (discovery.assumptions && discovery.assumptions.length > 0) {
      var aHtml = "<ul>";
      discovery.assumptions.forEach(function (a, idx) {
        aHtml += "<li><strong>Assumption:</strong> " + escapeHtml(a.assumption) + " (confidence: " + escapeHtml(a.confidence) + ")";
        if (a.question_for_user) {
          aHtml += '<br><em>Question: ' + escapeHtml(a.question_for_user) + "</em>";
          aHtml += '<br><textarea class="gate-assumption-answer" data-assumption-idx="' + idx + '" placeholder="Your answer (optional)..."></textarea>';
        }
        aHtml += "</li>";
      });
      aHtml += "</ul>";
      content += buildGateSectionHtml("Assumptions", aHtml);
    }

    // Open questions
    if (discovery.open_questions && discovery.open_questions.length > 0) {
      var qHtml = "";
      discovery.open_questions.forEach(function (q, idx) {
        qHtml += '<div class="gate-question">';
        qHtml += '<div class="gate-question-text">Q' + (idx + 1) + ': ' + escapeHtml(q) + '</div>';
        qHtml += '<textarea class="gate-answer" data-question-idx="' + idx + '" placeholder="Your answer (optional)..."></textarea>';
        qHtml += '</div>';
      });
      content += buildGateSectionHtml("Open Questions", qHtml);
    }

    // Free-text reviewer comments
    content += '<div class="gate-section">';
    content += '<h4>Reviewer Comments</h4>';
    content += '<p style="font-size:13px;color:var(--color-text-muted);margin:0 0 6px;">Additional observations, notes, or context not covered by the questions above. These will be included as context for downstream agents.</p>';
    content += '<textarea id="gate1-comment" rows="4" style="width:100%;font-size:14px;padding:8px;border:1px solid var(--color-border);border-radius:4px;resize:vertical;" placeholder="Any additional comments, observations, or context..."></textarea>';
    content += '</div>';

    // Actions
    var correctDisabled = gate1CorrectionCount >= 3;
    content += '<div class="gate-actions">';
    content += '<button class="btn btn-success" id="gate1-confirm">Confirm</button>';
    content += '<button class="btn btn-primary" id="gate1-correct"' + (correctDisabled ? " disabled" : "") + '>Correct</button>';
    if (correctDisabled) {
      content += '<span style="font-size:12px;color:var(--color-text-muted);">Correction limit reached (3/3)</span>';
    }
    content += '<button class="btn btn-danger" id="gate1-cancel">Cancel</button>';
    content += "</div>";

    panel.innerHTML = header + content;
    container.appendChild(panel);

    // Wire the copy button
    var copyBtn = panel.querySelector("#gate1-copy");
    if (copyBtn) {
      copyBtn.addEventListener("click", function () {
        var textContent = panel.innerText || panel.textContent;
        navigator.clipboard.writeText(textContent).then(function () {
          copyBtn.textContent = "Copied!";
          setTimeout(function () { copyBtn.textContent = "Copy"; }, 2000);
        }).catch(function () {
          // Fallback: select all text in the panel
          var range = document.createRange();
          range.selectNodeContents(panel);
          window.getSelection().removeAllRanges();
          window.getSelection().addRange(range);
        });
      });
    }

    // Pre-fill textareas with previous corrections if available.
    // Matches by question/assumption text hash so answers survive question reordering.
    if (previousCorrections) {
      var ua = previousCorrections.user_answers || {};
      var oqAnswers = ua.open_questions || {};
      $$(".gate-question", panel).forEach(function (qDiv) {
        var qTextEl = qDiv.querySelector(".gate-question-text");
        if (!qTextEl) return;
        var qText = qTextEl.textContent.trim();
        var qHash = simpleHash(qText);
        var ta = qDiv.querySelector(".gate-answer");
        if (!ta) return;
        // Try hash-keyed match first (new format)
        if (oqAnswers[qHash]) {
          var entry = oqAnswers[qHash];
          ta.value = typeof entry === "object" ? entry.answer : entry;
        } else {
          // Fallback: try legacy index-based match
          var idx = ta.dataset.questionIdx;
          if (oqAnswers[idx]) {
            var legacyEntry = oqAnswers[idx];
            ta.value = typeof legacyEntry === "object" ? legacyEntry.answer : legacyEntry;
          }
        }
      });

      var asAnswers = ua.assumptions || {};
      $$(".gate-assumption-answer", panel).forEach(function (ta) {
        var li = ta.closest("li");
        if (!li) return;
        var strongEl = li.querySelector("strong");
        var aText = strongEl && strongEl.nextSibling ? strongEl.nextSibling.textContent.trim() : "";
        var aHash = simpleHash(aText);
        // Try hash-keyed match first (new format)
        if (asAnswers[aHash]) {
          var entry = asAnswers[aHash];
          ta.value = typeof entry === "object" ? entry.answer : entry;
        } else {
          // Fallback: try legacy index-based match
          var idx = ta.dataset.assumptionIdx;
          if (asAnswers[idx]) {
            var legacyEntry = asAnswers[idx];
            ta.value = typeof legacyEntry === "object" ? legacyEntry.answer : legacyEntry;
          }
        }
      });
    }

    // Restore saved form state from localStorage (survives refresh/restart).
    // Applied after pre-fill from corrections so user's latest input wins.
    var savedGate1 = gateFormLoad(taskId, "gate1");
    restoreGate1FormState(panel, savedGate1);

    // Install auto-save on every input/change.
    installGateAutoSave(panel, taskId, "gate1", collectGate1FormState);

    // Enable inline editing on editable fields
    $$(".gate-editable", panel).forEach(function (field) {
      field.addEventListener("dblclick", function () {
        field.contentEditable = "true";
        field.focus();
      });
      field.addEventListener("blur", function () {
        field.contentEditable = "false";
      });
    });

    // Wire actions
    $("#gate1-confirm").addEventListener("click", function () {
      // Collect answers to open questions, keyed by question text hash
      var questionAnswers = {};
      $$(".gate-answer", panel).forEach(function (ta) {
        var text = ta.value.trim();
        if (text) {
          var qDiv = ta.closest(".gate-question");
          var qText = qDiv ? qDiv.querySelector(".gate-question-text").textContent.trim() : "";
          var key = qText ? simpleHash(qText) : ta.dataset.questionIdx;
          questionAnswers[key] = { question: qText, answer: text };
        }
      });

      // Collect answers to assumption questions, keyed by assumption text hash
      var assumptionAnswers = {};
      $$(".gate-assumption-answer", panel).forEach(function (ta) {
        var text = ta.value.trim();
        if (text) {
          var li = ta.closest("li");
          var aText = li ? li.querySelector("strong").nextSibling.textContent.trim() : "";
          var key = aText ? simpleHash(aText) : ta.dataset.assumptionIdx;
          assumptionAnswers[key] = { assumption: aText, answer: text };
        }
      });

      var payload = { action: "confirm" };
      if (Object.keys(questionAnswers).length > 0 || Object.keys(assumptionAnswers).length > 0) {
        payload.user_answers = {
          open_questions: questionAnswers,
          assumptions: assumptionAnswers
        };
      }
      var comment = ($("#gate1-comment") || {}).value;
      if (comment && comment.trim()) {
        payload.comment = comment.trim();
      }

      fetchJSON("/api/tasks/" + encodeURIComponent(taskId) + "/approve", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      }).then(function () {
        gateFormClear(taskId, "gate1");
        clearChildren(container);
      }).catch(function (err) {
        alert("Gate 1 confirm failed: " + err.message);
      });
    });

    $("#gate1-correct").addEventListener("click", function () {
      // Collect inline-edited fields
      var corrections = {};
      $$(".gate-editable", panel).forEach(function (field) {
        var key = field.dataset.field;
        if (key) corrections[key] = field.textContent;
      });

      // Collect answers to open questions, keyed by question text hash
      var questionAnswers = {};
      $$(".gate-answer", panel).forEach(function (ta) {
        var text = ta.value.trim();
        if (text) {
          var qDiv = ta.closest(".gate-question");
          var qText = qDiv ? qDiv.querySelector(".gate-question-text").textContent.trim() : "";
          var key = qText ? simpleHash(qText) : ta.dataset.questionIdx;
          questionAnswers[key] = { question: qText, answer: text };
        }
      });

      // Collect answers to assumption questions, keyed by assumption text hash
      var assumptionAnswers = {};
      $$(".gate-assumption-answer", panel).forEach(function (ta) {
        var text = ta.value.trim();
        if (text) {
          var li = ta.closest("li");
          var aText = li ? li.querySelector("strong").nextSibling.textContent.trim() : "";
          var key = aText ? simpleHash(aText) : ta.dataset.assumptionIdx;
          assumptionAnswers[key] = { assumption: aText, answer: text };
        }
      });

      var payload = { action: "correct", corrections: corrections };
      if (Object.keys(questionAnswers).length > 0 || Object.keys(assumptionAnswers).length > 0) {
        payload.user_answers = {
          open_questions: questionAnswers,
          assumptions: assumptionAnswers
        };
      }
      var comment = ($("#gate1-comment") || {}).value;
      if (comment && comment.trim()) {
        payload.comment = comment.trim();
      }

      fetchJSON("/api/tasks/" + encodeURIComponent(taskId) + "/approve", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      }).then(function () {
        gateFormClear(taskId, "gate1");
        gate1CorrectionCount++;
        clearChildren(container);
      }).catch(function (err) {
        alert("Gate 1 correction failed: " + err.message);
      });
    });

    $("#gate1-cancel").addEventListener("click", function () {
      fetchJSON("/api/tasks/" + encodeURIComponent(taskId) + "/reject", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ action: "cancel" })
      }).then(function () {
        gateFormClear(taskId, "gate1");
        clearChildren(container);
      }).catch(function (err) {
        alert("Gate 1 cancel failed: " + err.message);
      });
    });
  }

  function buildGateSection(label, value, editable) {
    var editClass = editable ? ' class="gate-editable" data-field="' + label.toLowerCase().replace(/\s+/g, "_") + '"' : "";
    return '<div class="gate-section">' +
      '<div class="gate-section-label">' + escapeHtml(label) + "</div>" +
      '<div class="gate-section-value"' + editClass + ">" + escapeHtml(value || "-") + "</div>" +
      "</div>";
  }

  function buildGateSectionHtml(label, html) {
    return '<div class="gate-section">' +
      '<div class="gate-section-label">' + escapeHtml(label) + "</div>" +
      '<div class="gate-section-value">' + html + "</div>" +
      "</div>";
  }

  // --- Gate 2: Ambiguity Resolution ---

  function showGate2Panel(data) {
    var container = $("#gate-panels");
    clearChildren(container);

    var drafter = data.data || {};
    var taskId = data.task_id || "";
    var warnings = drafter.ambiguity_warnings || [];

    if (warnings.length === 0) {
      // No ambiguities — auto-approve
      fetchJSON("/api/tasks/" + encodeURIComponent(taskId) + "/approve", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ resolutions: [] })
      }).catch(function () {});
      return;
    }

    var panel = el("div", { className: "gate-panel" });
    var header = '<h3><span class="gate-badge">Human Gate 2</span> Ambiguity Resolution</h3>';
    var content = "";

    content += '<table class="ambiguity-table">';
    content += "<thead><tr><th>ID</th><th>Section</th><th>Ambiguity</th><th>Agent Assumption</th><th>Question</th><th>Action</th><th>Answer</th></tr></thead>";
    content += "<tbody>";

    warnings.forEach(function (w, idx) {
      content += "<tr>" +
        "<td>" + escapeHtml(w.id) + "</td>" +
        "<td>" + escapeHtml(w.section) + "</td>" +
        "<td>" + escapeHtml(w.ambiguity) + "</td>" +
        "<td>" + escapeHtml(w.agent_assumption) + "</td>" +
        "<td>" + escapeHtml(w.question_for_user) + "</td>" +
        '<td><select class="amb-action" data-idx="' + idx + '">' +
        '<option value="accept">Accept assumption</option>' +
        '<option value="answer"' + (gate2AnswerDisabled ? " disabled" : "") + '>Provide answer</option>' +
        '<option value="defer">Defer</option>' +
        "</select></td>" +
        '<td><input class="answer-input amb-answer" data-idx="' + idx + '" type="text" placeholder="Your answer..."' + (gate2AnswerDisabled ? " disabled" : " disabled") + "></td>" +
        "</tr>";
    });

    content += "</tbody></table>";

    if (gate2AnswerDisabled) {
      content += '<p style="font-size:12px;color:var(--color-text-muted);">"Provide answer" is disabled — redraft limit reached.</p>';
    }

    // Free-text reviewer comments
    content += '<div class="gate-section" style="margin-top:12px;">';
    content += '<h4>Reviewer Comments</h4>';
    content += '<p style="font-size:13px;color:var(--color-text-muted);margin:0 0 6px;">Additional observations, notes, or context not covered above.</p>';
    content += '<textarea id="gate2-comment" rows="4" style="width:100%;font-size:14px;padding:8px;border:1px solid var(--color-border);border-radius:4px;resize:vertical;" placeholder="Any additional comments, observations, or context..."></textarea>';
    content += '</div>';

    content += '<div class="gate-actions">';
    content += '<button class="btn btn-success" id="gate2-submit">Submit Resolutions</button>';
    content += "</div>";

    panel.innerHTML = header + content;
    container.appendChild(panel);

    // Restore saved form state from localStorage.
    var savedGate2 = gateFormLoad(taskId, "gate2");
    restoreGate2FormState(panel, savedGate2);

    // Install auto-save on every input/change.
    installGateAutoSave(panel, taskId, "gate2", collectGate2FormState);

    // Enable/disable answer input based on action selection
    $$(".amb-action", panel).forEach(function (sel) {
      sel.addEventListener("change", function () {
        var idx = sel.dataset.idx;
        var input = $(".amb-answer[data-idx='" + idx + "']", panel);
        if (sel.value === "answer" && !gate2AnswerDisabled) {
          input.disabled = false;
          input.focus();
        } else {
          input.disabled = true;
          input.value = "";
        }
      });
    });

    // Submit
    $("#gate2-submit").addEventListener("click", function () {
      var resolutions = [];
      $$(".amb-action", panel).forEach(function (sel) {
        var idx = parseInt(sel.dataset.idx, 10);
        var w = warnings[idx];
        var action = sel.value;
        var answer = "";
        if (action === "answer") {
          answer = $(".amb-answer[data-idx='" + idx + "']", panel).value.trim();
        }
        resolutions.push({
          warning_id: w.id,
          action: action,
          answer: answer
        });
      });

      var gate2Payload = { resolutions: resolutions };
      var gate2Comment = ($("#gate2-comment") || {}).value;
      if (gate2Comment && gate2Comment.trim()) {
        gate2Payload.comment = gate2Comment.trim();
      }

      fetchJSON("/api/tasks/" + encodeURIComponent(taskId) + "/approve", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(gate2Payload)
      }).then(function () {
        gateFormClear(taskId, "gate2");
        // Disable answer after first re-draft
        var hasAnswer = resolutions.some(function (r) { return r.action === "answer"; });
        if (hasAnswer) gate2AnswerDisabled = true;
        clearChildren(container);
      }).catch(function (err) {
        alert("Gate 2 submission failed: " + err.message);
      });
    });
  }

  // -----------------------------------------------------------------------
  // Circuit Breaker / Agent Error Alerts
  // -----------------------------------------------------------------------

  function onCircuitBreaker(data) {
    addAlertBanner("circuit", "Circuit breaker: <strong>" + escapeHtml(data.breaker) +
      "</strong> — value: " + escapeHtml(String(data.value)) +
      ", limit: " + escapeHtml(String(data.limit)));
  }

  function onAgentError(data) {
    addAlertBanner("error", "Agent error: <strong>" + escapeHtml(data.agent) +
      "</strong> — " + escapeHtml(data.error_type) +
      " (retry " + data.retry_count + "/" + data.max_retries + ")");
  }

  function addAlertBanner(type, html) {
    var container = $("#alert-banners");
    var banner = el("div", { className: "alert-banner alert-banner-" + type });
    banner.innerHTML = '<span>' + html + '</span><button class="dismiss-banner">&times;</button>';
    $(".dismiss-banner", banner).addEventListener("click", function () { banner.remove(); });
    container.appendChild(banner);

    // Auto-dismiss after 30s
    setTimeout(function () {
      if (banner.parentNode) banner.remove();
    }, 30000);
  }

  // -----------------------------------------------------------------------
  // Messages Tab
  // -----------------------------------------------------------------------

  function addMessage(source, content, severity) {
    var container = $("#messages-container");
    if (!container) return;

    var now = new Date();
    var timestamp = String(now.getHours()).padStart(2, "0") + ":" +
                    String(now.getMinutes()).padStart(2, "0") + ":" +
                    String(now.getSeconds()).padStart(2, "0");

    var sevClass = severity ? " msg-" + severity : "";
    var sourceClass = "msg-source-" + source.replace(/[\[\]]/g, "");

    var entry = el("div", { className: "msg-entry" + sevClass, "data-source": source }, [
      el("span", { className: "msg-timestamp", textContent: timestamp }),
      el("span", { className: "msg-source " + sourceClass, textContent: "[" + source + "]" }),
      el("span", { className: "msg-content", textContent: content })
    ]);

    container.appendChild(entry);

    // Apply current filter
    applyMessagesFilter(entry);

    // Auto-scroll
    var autoScroll = $("#msg-auto-scroll");
    if (autoScroll && autoScroll.checked) {
      container.scrollTop = container.scrollHeight;
    }
  }

  function applyMessagesFilter(entry) {
    var filterSelect = $("#msg-filter");
    var filterVal = filterSelect ? filterSelect.value : "";
    if (!filterVal) {
      entry.classList.remove("msg-hidden");
      return;
    }
    var entrySource = entry.getAttribute("data-source") || "";
    if (entrySource === filterVal || entrySource.indexOf(filterVal) !== -1) {
      entry.classList.remove("msg-hidden");
    } else {
      entry.classList.add("msg-hidden");
    }
  }

  function applyAllMessagesFilter() {
    var container = $("#messages-container");
    if (!container) return;
    $$(".msg-entry", container).forEach(applyMessagesFilter);
  }

  function addWsEventToMessages(envelope) {
    var data = envelope.data || {};
    switch (envelope.event) {
      case "state_transition":
        addMessage("state", (data.from || "?") + " -> " + (data.to || "?") + " (round " + (data.round || "?") + ")");
        break;
      case "agent_dispatch":
        addMessage("agent", "Dispatching " + (data.agent || "?") + " agent");
        break;
      case "agent_complete":
        if (data.success) {
          var details = [];
          if (data.duration_ms != null) details.push((data.duration_ms / 1000).toFixed(1) + "s");
          if (data.cost_usd != null) details.push(formatCost(data.cost_usd));
          addMessage("agent", (data.agent || "?") + " completed" + (details.length ? " (" + details.join(", ") + ")" : ""), "success");
        } else {
          addMessage("agent", (data.agent || "?") + " FAILED", "error");
        }
        break;
      case "agent_metrics":
        addMessage("otel", "Tokens: in=" + (data.input_tokens || 0) + " out=" + (data.output_tokens || 0) + " cache=" + (data.cache_read_tokens || 0) + " | Cost: " + formatCost(data.total_cost_usd) + " | API calls: " + (data.total_api_calls || 0));
        break;
      case "agent_tool_event":
        var toolStatus = data.success ? "success" : "error";
        var toolMsg = "Tool: " + (data.tool_name || "?") + " (" + Math.round(data.duration_ms || 0) + "ms) " + (data.success ? "OK" : "FAILED");
        addMessage("otel", toolMsg, toolStatus);
        break;
      case "agent_api_event":
        var apiDetails = [];
        if (data.duration_ms) apiDetails.push((data.duration_ms / 1000).toFixed(1) + "s");
        if (data.cost_usd) apiDetails.push(formatCost(data.cost_usd));
        if (data.input_tokens || data.output_tokens) apiDetails.push((data.input_tokens || 0) + " in / " + (data.output_tokens || 0) + " out");
        addMessage("otel", "API: " + (data.model || "?") + (apiDetails.length ? " (" + apiDetails.join(", ") + ")" : ""));
        break;
      case "workflow_status":
        addMessage("orchestrator", "State: " + (data.state || "?") + " | Round " + (data.round || "?") + " | Cost " + formatCost(data.cost_usd) + " | " + (data.agent_invocations || 0) + " agents");
        break;
      case "circuit_breaker":
        addMessage("orchestrator", "Circuit breaker: " + (data.breaker || "?") + " (value=" + data.value + ", limit=" + data.limit + ")", "warning");
        break;
      case "agent_error":
        addMessage("agent", "Error: " + (data.agent || "?") + " - " + (data.error_type || "?") + " (retry " + (data.retry_count || 0) + "/" + (data.max_retries || 0) + ")", "error");
        break;
      case "gate_request":
        addMessage("state", "Human gate: " + (data.gate_type || "?"));
        break;
      case "gate_response":
        var severity = data.action === "cancel" ? "error" : (data.action === "correct" ? "warning" : "info");
        addMessage("state", "Gate response: " + (data.action || "?") + " — " + (data.detail || ""), severity);
        break;
    }
  }

  function loadServerLogs() {
    fetch("/api/logs/server").then(function (resp) {
      if (!resp.ok) return;
      return resp.json();
    }).then(function (lines) {
      if (!lines || !Array.isArray(lines)) return;
      // Only add new lines since last fetch
      var newLines = lines.slice(lastServerLogCount);
      lastServerLogCount = lines.length;

      newLines.forEach(function (line) {
        var source = "server";
        var severity = "";

        // Parse [tag] prefix to assign source coloring
        var tagMatch = line.match(/^\[([^\]]+)\]/);
        if (tagMatch) {
          var tag = tagMatch[1].toLowerCase();
          if (tag === "claude-runner") source = "claude-runner";
          else if (tag === "orchestrator" || tag === "workflow") source = "orchestrator";
          else if (tag.indexOf("otel") !== -1 || tag.indexOf("otlp") !== -1) source = "otel";
          else if (tag.indexOf("agent") !== -1) source = "agent";
        }

        // Detect severity from content
        if (/error|fail|fatal/i.test(line)) severity = "error";
        else if (/warn/i.test(line)) severity = "warning";

        addMessage(source, line, severity);
      });
    }).catch(function () {});
  }

  function startMessagesPolling() {
    // Load initial server logs
    loadServerLogs();
    // Poll every 2s
    if (messagesPollingTimer) clearInterval(messagesPollingTimer);
    messagesPollingTimer = setInterval(loadServerLogs, 2000);
  }

  function stopMessagesPolling() {
    if (messagesPollingTimer) {
      clearInterval(messagesPollingTimer);
      messagesPollingTimer = null;
    }
  }

  function initMessages() {
    // Clear button
    var clearBtn = $("#msg-clear");
    if (clearBtn) {
      clearBtn.addEventListener("click", function () {
        var container = $("#messages-container");
        clearChildren(container);
        lastServerLogCount = 0;
      });
    }

    // Filter dropdown
    var filterSelect = $("#msg-filter");
    if (filterSelect) {
      filterSelect.addEventListener("change", applyAllMessagesFilter);
    }
  }

  // -----------------------------------------------------------------------
  // Workflow Control Buttons (Reset)
  // -----------------------------------------------------------------------

  function initWorkflowControls() {
    var resetBtn = $("#btn-reset-workflow");
    if (resetBtn) {
      resetBtn.addEventListener("click", function () {
        var featureName = $("#goal-feature-name").value.trim();
        if (!featureName) {
          alert("Enter a feature name in the form above first.");
          return;
        }
        if (!confirm("Reset workflow for '" + featureName + "'? This deletes all spec files and state.")) return;
        fetchJSON("/api/workflow/reset", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ feature_name: featureName })
        }).then(function () {
          // Clear the workflow status panel
          var panel = $("#workflow-status");
          if (panel) panel.hidden = true;
          var badge = $("#workflow-state");
          if (badge) { badge.textContent = "IDLE"; badge.className = "state-badge state-badge-idle"; }
          addActivityEntry("Workflow reset for " + featureName, "info");
          loadFeatureList();
        }).catch(function (err) {
          alert("Reset failed: " + err.message);
        });
      });
    }
  }

  // -----------------------------------------------------------------------
  // Cancel Workflow
  // -----------------------------------------------------------------------

  function initCancelButton() {
    $("#btn-cancel-workflow").addEventListener("click", function () {
      if (!confirm("Cancel the running workflow?")) return;
      fetchJSON("/api/workflow/cancel", { method: "POST" })
        .then(function () { alert("Workflow cancelled."); })
        .catch(function (err) { alert("Cancel failed: " + err.message); });
    });
  }

  // -----------------------------------------------------------------------
  // Init
  // -----------------------------------------------------------------------

  function init() {
    initTabs();
    initGoalForm();
    initUpload();
    initSpecControls();
    initIssueFilters();
    initCancelButton();
    initMessages();
    initWorkflowControls();
    wsConnect();
    // Load workspace browser on startup and start auto-refresh
    loadFeatureList();
    startFeatureListPolling();

    // Check if we need to show a gate panel on page load (e.g. after refresh).
    // Also restore persisted OTEL metrics so dashboard data survives refresh.
    fetchJSON("/api/workflow/status").then(function (status) {
      if (!status || !status.state) return;
      var state = status.state.toUpperCase();
      var feature = status.feature_name;
      if (!feature) return;

      // Restore persisted metrics for the active workflow.
      if (state !== "IDLE") {
        restorePersistedMetrics(feature);
      }

      if (state === "HUMAN_GATE_1") {
        // Fetch discovery output and any previous corrections in parallel.
        Promise.all([
          fetchJSON("/api/workspace/features/" + encodeURIComponent(feature) + "/discovery").catch(function () { return null; }),
          fetchJSON("/api/workspace/features/" + encodeURIComponent(feature) + "/files/gate1-corrections.json").catch(function () { return null; })
        ]).then(function (results) {
          var discovery = results[0];
          var corrections = results[1];
          if (discovery) {
            showGate1Panel({ gate_type: "requirements_confirmation", data: discovery, task_id: feature }, corrections);
          }
        });
      } else if (state === "HUMAN_GATE_2") {
        fetchJSON("/api/workspace/features/" + encodeURIComponent(feature) + "/files/drafter-output.json").then(function (drafter) {
          if (drafter) {
            showGate2Panel({ gate_type: "ambiguity_resolution", data: drafter, task_id: feature });
          }
        }).catch(function () {});
      }
    }).catch(function () {});
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
