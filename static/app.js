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
  var taskCache = [];
  var convergenceHistory = [];
  var lensSet = new Set();

  // Currently selected workflow (feature name) — drives all panel updates.
  var selectedFeature = null;
  // Workflow type of the currently selected workflow ("spec" or "code_review").
  var selectedWorkflowType = "spec";
  // Cached array of all workflow statuses from /api/workflow/status.
  var allWorkflowStatuses = [];

  // Gate state
  var gate1CorrectionCount = 0;
  var gate2AnswerDisabled = false;
  // Prevents gate panel re-render immediately after a submit while waiting
  // for the orchestrator to transition away from the gate state.
  var gatePanelSubmitting = false;

  // Notification badges for unselected workflows at gate states.
  // Maps featureName -> true when a workflow needs operator attention.
  var workflowBadges = {};

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
        else if (k === "htmlFor") node.setAttribute("for", attrs[k]);
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
      case "TASK_HUMAN_GATE":
        return "state-badge-purple";
      case "TASKIFY":
      case "TASK_REVIEW":
      case "TASK_REVISION":
      case "TASKS_APPROVED":
        return "state-badge-orange";
      case "FINALIZED":
        return "state-badge-blue";
      case "COMPLETE":
        return "state-badge-green";
      case "ESCALATED":
      case "ERROR":
      case "CR_ESCALATED":
        return "state-badge-red";
      case "CR_INIT":
        return "state-badge-blue";
      case "CR_HUMAN_GATE_SCOPE":
      case "CR_HUMAN_GATE_FIXES":
        return "state-badge-purple";
      case "CR_REVIEWING":
      case "CR_FIXING":
        return "state-badge-orange";
      case "CR_COMPLETE":
        return "state-badge-green";
      default:
        return "state-badge-idle";
    }
  }

  // -----------------------------------------------------------------------
  // Workflow Pipeline (state machine stepper)
  // -----------------------------------------------------------------------

  // The ordered stages of the happy-path workflow.
  // Labels are shortened for space; gates show a gate icon.
  var PIPELINE_STAGES = [
    { state: "INIT",             label: "Init",     gate: false },
    { state: "DISCOVERY",        label: "Discover",  gate: false },
    { state: "HUMAN_GATE_1",     label: "Gate 1",   gate: true  },
    { state: "DRAFTING",         label: "Draft",    gate: false },
    { state: "HUMAN_GATE_2",     label: "Gate 2",   gate: true  },
    { state: "REVIEWING",        label: "Review",   gate: false },
    { state: "HOLDOUT",          label: "Holdout",  gate: false },
    { state: "REVISING",         label: "Revise",   gate: false },
    { state: "JUDGING",          label: "Judge",    gate: false },
    { state: "HUMAN_GATE_FINAL", label: "Gate F",   gate: true  },
    { state: "FINALIZED",        label: "Finalized", gate: false },
    { state: "TASKIFY",          label: "Taskify",   gate: false },
    { state: "TASK_REVIEW",      label: "T.Review",  gate: false },
    { state: "TASK_REVISION",    label: "T.Revision", gate: false },
    { state: "TASK_HUMAN_GATE",  label: "T.Gate",    gate: true  },
    { state: "COMPLETE",         label: "Done",     gate: false }
  ];

  var CR_PIPELINE_STAGES = [
    { state: "CR_INIT",             label: "Init",    gate: false },
    { state: "CR_HUMAN_GATE_SCOPE", label: "Scope",   gate: true  },
    { state: "CR_REVIEWING",        label: "Review",  gate: false },
    { state: "CR_FIXING",           label: "Fix",     gate: false },
    { state: "CR_HUMAN_GATE_FIXES", label: "Fixes",   gate: true  },
    { state: "CR_COMPLETE",         label: "Done",    gate: false }
  ];

  var CD_PIPELINE_STAGES = [
    { state: "CD_INIT",              label: "Init",         gate: false },
    { state: "CD_DISCOVERY",         label: "Discover",     gate: false },
    { state: "CD_HUMAN_GATE_SCOPE",  label: "Scope",        gate: true  },
    { state: "CD_DRAFTING",          label: "Draft",        gate: false },
    { state: "CD_SANITISING",        label: "Sanitise",     gate: false },
    { state: "CD_HUMAN_GATE_DRAFT",  label: "Review Draft", gate: true  },
    { state: "CD_REVIEWING",         label: "Review",       gate: false },
    { state: "CD_REVISING",          label: "Revise",       gate: false },
    { state: "CD_JUDGING",           label: "Judge",        gate: false },
    { state: "CD_HUMAN_GATE_FINAL",  label: "Final",        gate: true  },
    { state: "CD_WRITING",           label: "Write",        gate: false },
    { state: "CD_COMPLETE",          label: "Done",         gate: false }
  ];

  /**
   * Renders or updates the workflow pipeline stepper.
   * @param {string} currentState - The current workflow state (e.g. "REVIEWING").
   * @param {string} [workflowType] - "spec" or "code_review".
   */
  function updateWorkflowPipeline(currentState, workflowType) {
    var container = $("#workflow-pipeline");
    if (!container) return;

    var state = (currentState || "IDLE").toUpperCase();
    var stages = PIPELINE_STAGES;
    if (workflowType === "code_review") stages = CR_PIPELINE_STAGES;
    else if (workflowType === "codedoc") stages = CD_PIPELINE_STAGES;

    // Find the index of the current state in the pipeline (-1 if not in happy path).
    var currentIdx = getPipelineStageIndex(state, workflowType, stages);
    for (var i = 0; currentIdx === -1 && i < stages.length; i++) {
      if (stages[i].state === state) {
        currentIdx = i;
        break;
      }
    }

    // For ERROR/ESCALATED, find the furthest stage reached
    // by looking at the pipeline and marking everything up to current as completed.
    var isError = state === "ERROR" || state === "CD_ERROR";
    var isEscalated = state === "ESCALATED" || state === "CR_ESCALATED" || state === "CD_ESCALATED";

    // Build the pipeline HTML
    container.innerHTML = "";
    for (var j = 0; j < stages.length; j++) {
      var stage = stages[j];

      // Connector between steps (skip before first)
      if (j > 0) {
        var conn = document.createElement("div");
        conn.className = "pipeline-connector";
        if (currentIdx >= 0 && j <= currentIdx) {
          conn.classList.add("completed");
        }
        container.appendChild(conn);
      }

      // Node
      var node = document.createElement("div");
      node.className = "pipeline-node";
      node.textContent = stage.label;

      if (stage.gate) {
        node.classList.add("gate");
      }

      if (currentIdx >= 0) {
        if (j < currentIdx) {
          node.classList.add("completed");
        } else if (j === currentIdx) {
          node.classList.add("current");
        }
      }

      var step = document.createElement("div");
      step.className = "pipeline-step";
      step.appendChild(node);
      container.appendChild(step);
    }

    // If ERROR or ESCALATED, append a special terminal node
    if (isError || isEscalated) {
      var connTerm = document.createElement("div");
      connTerm.className = "pipeline-connector";
      container.appendChild(connTerm);

      var termNode = document.createElement("div");
      termNode.className = "pipeline-node current";
      termNode.classList.add(isError ? "error" : "escalated");
      termNode.textContent = isError ? "Error" : "Escalated";
      var termStep = document.createElement("div");
      termStep.className = "pipeline-step";
      termStep.appendChild(termNode);
      container.appendChild(termStep);
    }
  }

  function getPipelineStageIndex(state, workflowType, stages) {
    if (workflowType !== "spec") return -1;
    var specStageIndex = {
      "INIT": 0,
      "DISCOVERY": 1,
      "HUMAN_GATE_1": 2,
      "DRAFTING": 3,
      "HUMAN_GATE_2": 4,
      "REVIEWING": 5,
      "REVISING": 7,
      "JUDGING": 8,
      "HUMAN_GATE_FINAL": 9,
      "FINALIZED": 10,
      "TASKIFY": 11,
      "TASK_REVIEW": 12,
      "TASK_REVISION": 13,
      "TASK_HUMAN_GATE": 14,
      "COMPLETE": 15
    };
    if (Object.prototype.hasOwnProperty.call(specStageIndex, state)) {
      return specStageIndex[state];
    }
    for (var i = 0; i < stages.length; i++) {
      if (stages[i].state === state) return i;
    }
    return -1;
  }

  // -----------------------------------------------------------------------
  // Workflow Status List (Active Workflows Panel)
  // -----------------------------------------------------------------------

  /**
   * Returns a CSS class suffix for the workflow status list badge.
   *   INIT / DISCOVERY / DRAFTING → active (blue)
   *   HUMAN_GATE_* → gate (orange)
   *   REVIEWING / REVISING / JUDGING → review (purple)
   *   FINALIZED → done (green)
   *   ESCALATED / ERROR → error (red)
   *   idle / unknown → idle (gray)
   */
  function getWsiBadgeClass(state) {
    var s = (state || "").toUpperCase();
    switch (s) {
      case "INIT":
      case "DISCOVERY":
      case "DRAFTING":
        return "wsi-badge-active";
      case "HUMAN_GATE_1":
      case "HUMAN_GATE_2":
      case "HUMAN_GATE_FINAL":
      case "TASK_HUMAN_GATE":
        return "wsi-badge-gate";
      case "REVIEWING":
      case "REVISING":
      case "JUDGING":
      case "TASKIFY":
      case "TASK_REVIEW":
      case "TASK_REVISION":
      case "TASKS_APPROVED":
        return "wsi-badge-review";
      case "FINALIZED":
        return "wsi-badge-active";
      case "COMPLETE":
      case "CR_COMPLETE":
        return "wsi-badge-done";
      case "ESCALATED":
      case "ERROR":
      case "CR_ESCALATED":
        return "wsi-badge-error";
      case "CR_INIT":
        return "wsi-badge-active";
      case "CR_HUMAN_GATE_SCOPE":
      case "CR_HUMAN_GATE_FIXES":
        return "wsi-badge-gate";
      case "CR_REVIEWING":
      case "CR_FIXING":
        return "wsi-badge-review";
      case "CD_INIT":
      case "CD_DISCOVERY":
      case "CD_DRAFTING":
      case "CD_SANITISING":
      case "CD_WRITING":
        return "wsi-badge-active";
      case "CD_HUMAN_GATE_SCOPE":
      case "CD_HUMAN_GATE_DRAFT":
      case "CD_HUMAN_GATE_FINAL":
        return "wsi-badge-gate";
      case "CD_REVIEWING":
      case "CD_REVISING":
      case "CD_JUDGING":
        return "wsi-badge-review";
      case "CD_COMPLETE":
        return "wsi-badge-done";
      case "CD_ESCALATED":
      case "CD_ERROR":
        return "wsi-badge-error";
      default:
        return "wsi-badge-idle";
    }
  }

  /**
   * Renders the workflow status list panel from an array of status objects.
   * Each object has: feature_name, state, round, cost_usd, wall_clock_seconds,
   * agent_invocations, is_paused.
   */
  function renderWorkflowStatusList(statuses) {
    allWorkflowStatuses = statuses || [];
    var container = $("#workflow-status-items");
    if (!container) return;
    clearChildren(container);

    if (allWorkflowStatuses.length === 0) {
      container.appendChild(el("p", {
        className: "workflow-status-empty",
        textContent: "No active workflows. Start one below."
      }));
      return;
    }

    allWorkflowStatuses.forEach(function (wf) {
      var featureName = wf.feature_name || "unknown";
      var state = wf.state || "IDLE";
      var isSelected = selectedFeature === featureName;

      var item = el("div", {
        className: "workflow-status-item" + (isSelected ? " selected" : ""),
        "data-feature": featureName
      });

      // Feature name + type badge
      var nameSpan = el("span", { className: "wsi-name", textContent: featureName });
      var wfType = wf.workflow_type || "spec";
      var typeLabel = wfType === "code_review" ? "CR" : wfType === "codedoc" ? "CD" : "SPEC";
      var typeBadgeClass = "wsi-type-badge" + (wfType === "codedoc" ? " wsi-type-cd" : "");
      nameSpan.appendChild(document.createTextNode(" "));
      nameSpan.appendChild(el("span", { className: typeBadgeClass, textContent: typeLabel }));

      // Notification badge — red dot for unselected workflows needing attention
      if (workflowBadges[featureName] && !isSelected) {
        nameSpan.appendChild(el("span", { className: "wsi-notification-badge", title: "Needs attention" }));
      }

      item.appendChild(nameSpan);

      // State badge
      item.appendChild(el("span", {
        className: "wsi-badge " + getWsiBadgeClass(state),
        textContent: state
      }));

      // Agent running indicator
      if (wf.is_running) {
        item.appendChild(el("span", { className: "wsi-running-indicator" }, [
          el("span", { className: "wsi-running-dot" }),
          document.createTextNode("running")
        ]));
      } else {
        var stateUp = state.toUpperCase();
        var isTerminal = stateUp === "COMPLETE" || stateUp === "ESCALATED" || stateUp === "ERROR" ||
          stateUp === "CR_COMPLETE" || stateUp === "CR_ESCALATED" ||
          stateUp === "CD_COMPLETE" || stateUp === "CD_ESCALATED" || stateUp === "CD_ERROR";
        if (!isTerminal && stateUp !== "IDLE") {
          item.appendChild(el("span", { className: "wsi-not-running-indicator", textContent: "not running" }));
        }
      }

      // Metrics: cost, elapsed time
      var metrics = el("div", { className: "wsi-metrics" });
      metrics.appendChild(el("span", { className: "wsi-metric" }, [
        el("strong", { textContent: formatCost(wf.cost_usd) })
      ]));
      metrics.appendChild(el("span", { className: "wsi-metric" }, [
        el("strong", { textContent: formatDuration(wf.wall_clock_seconds) })
      ]));
      if (wf.round > 0) {
        metrics.appendChild(el("span", { className: "wsi-metric", textContent: "R" + wf.round }));
      }
      item.appendChild(metrics);

      // Action buttons
      var actionsDiv = el("div", { className: "wsi-actions" });
      var stateUpper = state.toUpperCase();
      var isTerminalState = stateUpper === "COMPLETE" || stateUpper === "ESCALATED" || stateUpper === "ERROR" ||
        stateUpper === "CR_COMPLETE" || stateUpper === "CR_ESCALATED" ||
        stateUpper === "CD_COMPLETE" || stateUpper === "CD_ESCALATED" || stateUpper === "CD_ERROR";

      if (isTerminalState) {
        // Reset button — clears workspace so user can start fresh with same feature name
        var resetBtn = el("button", { className: "btn btn-sm", textContent: "Reset", style: "background:#ffc107;color:#212529;border-color:#ffc107;" });
        resetBtn.addEventListener("click", (function (fname, workflowType) {
          return function (e) {
            e.stopPropagation();
            if (!confirm("Reset workflow \"" + fname + "\"? This clears the workspace so you can re-run it with the same feature name.")) return;
            resetBtn.disabled = true;
            runWorkflowReset(workflowType, fname).then(function () {
              addActivityEntry("Workflow reset: " + fname + " — start a new workflow with the same feature name", "info");
              if (selectedFeature === fname) selectedFeature = null;
              refreshWorkflowStatusList();
              loadFeatureList();
            }).catch(function (err) {
              alert("Reset failed: " + err.message);
              resetBtn.disabled = false;
            });
          };
        })(featureName, wfType));
        actionsDiv.appendChild(resetBtn);
      }

      // View/Selected button
      var viewBtn = el("button", {
        className: "btn btn-sm" + (isSelected ? " btn-primary" : ""),
        textContent: isSelected ? "Selected" : "View"
      });
      viewBtn.addEventListener("click", (function (fname) {
        return function (e) {
          e.stopPropagation();
          selectWorkflow(fname);
        };
      })(featureName));
      actionsDiv.appendChild(viewBtn);
      item.appendChild(actionsDiv);

      container.appendChild(item);
    });
  }

  /**
   * Selects a workflow as the active context for the dashboard.
   * Updates selectedFeature, highlights the list item, and refreshes
   * all panels to show data for the selected workflow.
   */
  function selectWorkflow(featureName) {
    var previousFeature = selectedFeature;
    selectedFeature = featureName;

    // Clear notification badge for the newly selected workflow.
    delete workflowBadges[featureName];

    // Re-render the status list to update selection highlight
    renderWorkflowStatusList(allWorkflowStatuses);

    // Update the workflow status detail panel with the selected workflow's data
    var match = null;
    for (var i = 0; i < allWorkflowStatuses.length; i++) {
      if (allWorkflowStatuses[i].feature_name === featureName) {
        match = allWorkflowStatuses[i];
        break;
      }
    }

    // Track workflow type and update tab visibility
    selectedWorkflowType = (match && match.workflow_type) || "spec";
    var specOnlyTabs = ["spec", "issues", "convergence"];
    $$(".nav-tab").forEach(function (btn) {
      var tab = btn.dataset.tab;
      if (specOnlyTabs.indexOf(tab) !== -1) {
        if (selectedWorkflowType === "code_review" || selectedWorkflowType === "codedoc") {
          btn.classList.add("tab-disabled");
        } else {
          btn.classList.remove("tab-disabled");
        }
      }
    });

    if (match) {
      updateWorkflowStatus(match);

      // Clear activity feed and agent metrics, then restore for the selected workflow.
      clearChildren($("#status-activity"));
      var metricsPanel = $("#agent-metrics");
      if (metricsPanel) metricsPanel.hidden = true;

      restorePersistedMetrics(featureName, true);
    }

    // Refresh all data tabs for the newly selected workflow
    refreshAllPanelsForFeature(featureName);

    // Show or hide gate panels based on the selected workflow's state
    refreshGatePanelsForFeature(featureName);

    // Dispatch a custom event so other parts of the app can react
    document.dispatchEvent(new CustomEvent("workflow-selected", {
      detail: { feature_name: featureName }
    }));
  }

  /**
   * Refreshes all data-driven panels (spec, issues, convergence) for
   * the given feature. Called when the selected workflow changes.
   */
  function refreshAllPanelsForFeature(featureName) {
    // Re-fetch spec tab data
    loadSpecVersions();
    loadCurrentSpec();

    // Re-fetch issues tab data
    loadIssues();

    // Re-fetch tasks tab data
    loadTasks();

    // Re-fetch convergence tab data
    loadConvergence();

    // Re-fetch workspace tab data
    loadWorkspaceFiles();
  }

  /**
   * Shows or hides gate panels based on the selected workflow's state.
   * If the selected workflow is at a gate state, fetches gate data and
   * renders the appropriate panel. Otherwise clears any existing gate panel.
   */
  function refreshGatePanelsForFeature(featureName) {
    var container = $("#gate-panels");
    if (!container) return;

    // Find the selected workflow's current state
    var match = null;
    for (var i = 0; i < allWorkflowStatuses.length; i++) {
      if (allWorkflowStatuses[i].feature_name === featureName) {
        match = allWorkflowStatuses[i];
        break;
      }
    }

    var state = match ? (match.state || "").toUpperCase() : "";

    // Clear existing gate panels first
    clearChildren(container);

    // Only show gate UI when the selected workflow is at a gate state
    if (state === "HUMAN_GATE_1") {
      Promise.all([
        fetchJSON("/api/workspace/features/" + encodeURIComponent(featureName) + "/discovery").catch(function () { return null; }),
        fetchJSON("/api/workspace/features/" + encodeURIComponent(featureName) + "/files/gate1-corrections.json").catch(function () { return null; })
      ]).then(function (results) {
        if (results[0]) {
          showGate1Panel({ gate_type: "requirements_confirmation", data: results[0], task_id: featureName }, results[1]);
        }
      });
    } else if (state === "HUMAN_GATE_2") {
      fetchJSON("/api/workspace/features/" + encodeURIComponent(featureName) + "/files/drafter-output.json").then(function (drafter) {
        if (drafter) {
          showGate2Panel({ gate_type: "ambiguity_resolution", data: drafter, task_id: featureName });
        }
      }).catch(function () {});
    } else if (state === "CR_HUMAN_GATE_SCOPE" || state === "CR_HUMAN_GATE_FIXES") {
      fetchJSON("/api/codereview/" + encodeURIComponent(featureName) + "/status").then(function (crStatus) {
        if (!crStatus) return;
        if (state === "CR_HUMAN_GATE_SCOPE") {
          showCRScopeGatePanel(featureName, crStatus);
        } else {
          showCRFixesGatePanel(featureName, crStatus);
        }
      }).catch(function () {});
    } else if (state === "CD_HUMAN_GATE_SCOPE") {
      fetchJSON("/api/codedoc/" + encodeURIComponent(featureName) + "/status").then(function (cdStatus) {
        if (cdStatus) showCDScopeGatePanel(featureName, cdStatus);
      }).catch(function () {});
    } else if (state === "CD_HUMAN_GATE_DRAFT") {
      fetchJSON("/api/codedoc/" + encodeURIComponent(featureName) + "/status").then(function (cdStatus) {
        if (cdStatus) showCDDraftGatePanel(featureName, cdStatus);
      }).catch(function () {});
    } else if (state === "CD_HUMAN_GATE_FINAL") {
      fetchJSON("/api/codedoc/" + encodeURIComponent(featureName) + "/status").then(function (cdStatus) {
        if (cdStatus) showCDFinalGatePanel(featureName, cdStatus);
      }).catch(function () {});
    } else if (state === "HUMAN_GATE_FINAL") {
      fetchJSON("/api/workspace/features/" + encodeURIComponent(featureName) + "/files").then(function (files) {
        var specFiles = (files || []).filter(function (f) { return /^spec-v\d+\.md$/.test(f.name); });
        specFiles.sort(function (a, b) {
          var na = parseInt(a.name.replace("spec-v", "").replace(".md", ""), 10);
          var nb = parseInt(b.name.replace("spec-v", "").replace(".md", ""), 10);
          return nb - na;
        });
        if (specFiles.length > 0) {
          fetchJSON("/api/workspace/features/" + encodeURIComponent(featureName) + "/files/" + encodeURIComponent(specFiles[0].name))
            .then(function (specData) { showFinalGatePanel(featureName, specFiles[0].name, specData); })
            .catch(function () { showFinalGatePanel(featureName, "", null); });
        } else {
          showFinalGatePanel(featureName, "", null);
        }
      }).catch(function () { showFinalGatePanel(featureName, "", null); });
    }
  }

  /**
   * Fetches all workflow statuses and renders the status list.
   * Called on init, on poll, and after WebSocket events.
   */
  function refreshWorkflowStatusList() {
    fetchJSON("/api/workflow/status").then(function (data) {
      if (!data) return;

      // The endpoint returns an array when no ?feature= param.
      var statuses = Array.isArray(data) ? data : [data];
      renderWorkflowStatusList(statuses);

      // If a workflow is selected, update the detail panel too.
      if (selectedFeature) {
        for (var i = 0; i < statuses.length; i++) {
          if (statuses[i].feature_name === selectedFeature) {
            updateWorkflowStatus(statuses[i]);
            // If at a gate state and no panel is showing, render it now.
            // This covers page-load and non-running gate workflows.
            // Skip if gatePanelSubmitting — we just cleared it intentionally.
            if (!$(".gate-panel") && !gatePanelSubmitting) {
              refreshGatePanelsForFeature(selectedFeature);
            }
            break;
          }
        }
      }
    }).catch(function () {
      // Endpoint not available — render empty
      renderWorkflowStatusList([]);
    });
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
    updateWorkflowPipeline(state, selectedWorkflowType);

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
    var shouldTick = upper !== "IDLE" && upper !== "COMPLETE" && upper !== "ESCALATED" && upper !== "CR_COMPLETE" && upper !== "CR_ESCALATED" && !data.paused;
    if (shouldTick) {
      startWallClockTimer();
    } else {
      stopWallClockTimer();
    }
  }

  // showResumeChoiceModal displays a lightweight modal offering the resume
  // modes reported by /api/workflow/resume-options. onPick is invoked with
  // the chosen mode string ("skip_to_gate" / "replay_merge" / "restart_fresh")
  // once the user clicks a button. If the user cancels, onPick is not called.
  //
  // Styling uses the same CSS custom properties as the rest of the app, so
  // the modal tracks whichever theme is active (see :root and
  // [data-theme="light"] in style.css).
  function showResumeChoiceModal(featureName, opts, onPick) {
    // Backdrop covers the viewport and closes the modal on click.
    var backdrop = el("div", {
      style: "position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.55);z-index:10000;display:flex;align-items:center;justify-content:center;"
    });
    var box = el("div", {
      style: [
        "background:var(--color-surface)",
        "color:var(--color-text)",
        "padding:24px",
        "border-radius:var(--radius-lg)",
        "max-width:560px",
        "width:92%",
        "box-shadow:var(--shadow-lg)",
        "border:1px solid var(--color-border)",
        "font-family:var(--font-ui)"
      ].join(";")
    });
    box.addEventListener("click", function (ev) { ev.stopPropagation(); });
    backdrop.addEventListener("click", function () { document.body.removeChild(backdrop); });

    box.appendChild(el("h3", {
      textContent: "Resume " + featureName,
      style: "margin:0 0 8px;font-size:18px;color:var(--color-text-strong);"
    }));
    var inferred = (opts && opts.inferred_stage) || "?";
    var persisted = (opts && opts.persisted_state) || "?";
    box.appendChild(el("div", {
      textContent: "Workflow state: " + persisted + "  —  Inferred stage: " + inferred,
      style: "color:var(--color-text-muted);font-size:13px;margin-bottom:14px;"
    }));

    // Locate the inferred stage entry and build one button per available mode.
    var stageOpt = null;
    if (opts && opts.stages) {
      for (var i = 0; i < opts.stages.length; i++) {
        if (opts.stages[i].stage === opts.inferred_stage) {
          stageOpt = opts.stages[i];
          break;
        }
      }
    }
    if (!stageOpt || !stageOpt.available_modes || stageOpt.available_modes.length === 0) {
      box.appendChild(el("div", {
        textContent: "No resume modes available — the workflow may be empty. Try Restart instead.",
        style: "color:var(--color-danger);margin-bottom:14px;"
      }));
    } else {
      // Preview summary of what's on disk.
      if (stageOpt.canonical_preview) {
        var p = stageOpt.canonical_preview;
        var summary = Object.keys(p).map(function (k) { return k + "=" + p[k]; }).join(", ");
        box.appendChild(el("div", {
          textContent: "Existing canonical output: " + summary,
          style: "color:var(--color-primary);font-size:12px;margin-bottom:14px;font-family:var(--font-mono);"
        }));
      }

      var modeDescriptions = {
        "skip_to_gate": {
          label: "Skip to gate",
          desc: "Accept existing outputs and advance to " + (stageOpt.next_gate || "the next gate") + " immediately.",
          className: "btn-success"
        },
        "replay_merge": {
          label: "Replay merge",
          desc: "Re-run the merge/combine step using the existing per-provider outputs, without re-dispatching drafters.",
          className: "btn-primary"
        },
        "restart_fresh": {
          label: "Restart stage",
          desc: "Re-run this stage's agents from scratch. Existing outputs will be overwritten.",
          className: "btn-warning"
        }
      };

      // Render buttons in canonical display order: skip, replay, restart.
      var displayOrder = ["skip_to_gate", "replay_merge", "restart_fresh"];
      displayOrder.forEach(function (mode) {
        if (stageOpt.available_modes.indexOf(mode) < 0) return;
        var meta = modeDescriptions[mode];
        var row = el("div", {
          style: [
            "display:flex",
            "gap:12px",
            "align-items:flex-start",
            "padding:10px",
            "margin-bottom:8px",
            "background:var(--color-surface-2)",
            "border-radius:var(--radius)",
            "border:1px solid var(--color-border)"
          ].join(";")
        });
        var btn = el("button", {
          className: "btn " + meta.className + " btn-sm",
          textContent: meta.label,
          style: "min-width:130px;flex-shrink:0;"
        });
        btn.addEventListener("click", function () {
          document.body.removeChild(backdrop);
          if (typeof onPick === "function") onPick(mode);
        });
        row.appendChild(btn);
        row.appendChild(el("div", {
          textContent: meta.desc,
          style: "color:var(--color-text);font-size:13px;"
        }));
        box.appendChild(row);
      });

      // Default-highlight the recommended mode.
      if (opts.default_mode) {
        var hint = el("div", {
          textContent: "Recommended: " + opts.default_mode,
          style: "color:var(--color-text-muted);font-size:12px;margin-top:4px;"
        });
        box.appendChild(hint);
      }
    }

    var cancelRow = el("div", { style: "text-align:right;margin-top:14px;" });
    // Use bare .btn — the neutral selector at style.css:832 themes it.
    var cancelBtn = el("button", { className: "btn btn-sm", textContent: "Cancel" });
    cancelBtn.addEventListener("click", function () { document.body.removeChild(backdrop); });
    cancelRow.appendChild(cancelBtn);
    box.appendChild(cancelRow);

    backdrop.appendChild(box);
    document.body.appendChild(backdrop);
  }

  function addActivityEntry(message, type, featureName) {
    var container = $("#status-activity");
    var typeClass = type ? "activity-" + type : "";
    var wf = featureName || "";

    var now = new Date();
    var timestamp = String(now.getHours()).padStart(2, "0") + ":" +
                    String(now.getMinutes()).padStart(2, "0") + ":" +
                    String(now.getSeconds()).padStart(2, "0");

    var children = [
      el("span", { className: "activity-time", textContent: timestamp })
    ];
    if (wf) {
      children.push(el("span", { className: "msg-workflow", textContent: wf, title: wf }));
    }
    children.push(el("span", { className: "activity-msg", textContent: message }));

    var entry = el("div", { className: "activity-entry " + typeClass }, children);

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
    updateWorkflowPipeline(state, selectedWorkflowType);

    // Clear active agents on state transition — any agents from the
    // previous state are done.
    activeAgents = {};
    renderActiveAgents();

    // Show the panel when a transition happens
    $("#workflow-status").hidden = false;

    if (data.round != null) {
      $("#status-round").textContent = data.round;
    }

    var msg = "State: " + (data.from || "?") + " -> " + state;
    if (data.round != null) msg += " (round " + data.round + ")";
    addActivityEntry(msg, "info", data.feature_name);

    // Note: workflow status list refresh is handled by handleEvent() which
    // calls refreshWorkflowStatusList() before dispatching to this handler.
  }

  // -----------------------------------------------------------------------
  // Active Agents Tracker
  // -----------------------------------------------------------------------

  // activeAgents tracks currently running agents: { agentName: { dispatched: timestamp, provider: "claude"|"codex" } }
  var activeAgents = {};

  function getAgentProvider(agentName) {
    if (agentName.endsWith("-codex")) return "codex";
    if (agentName.endsWith("-claude")) return "claude";
    return "claude";
  }

  function getAgentRole(agentName) {
    if (agentName.startsWith("reviewer-")) return "reviewer";
    if (agentName.startsWith("holdout-")) return "holdout";
    return agentName;
  }

  function renderActiveAgents() {
    var container = $("#active-agents-panel");
    if (!container) return;

    var agents = Object.keys(activeAgents);
    if (agents.length === 0) {
      container.style.display = "none";
      return;
    }

    container.style.display = "block";
    container.innerHTML = "";

    var header = el("div", { className: "active-agents-header" }, [
      document.createTextNode("Active Agents "),
      el("span", { className: "active-agents-count" }, [document.createTextNode("(" + agents.length + ")")])
    ]);
    container.appendChild(header);

    var grid = el("div", { className: "active-agents-grid" });

    // Sort agents: reviewers first, then holdouts, then others
    agents.sort(function(a, b) {
      var ra = getAgentRole(a), rb = getAgentRole(b);
      if (ra !== rb) {
        if (ra === "reviewer") return -1;
        if (rb === "reviewer") return 1;
        if (ra === "holdout") return -1;
        if (rb === "holdout") return 1;
      }
      return a.localeCompare(b);
    });

    for (var i = 0; i < agents.length; i++) {
      var name = agents[i];
      var info = activeAgents[name];
      var provider = info.provider;
      var role = getAgentRole(name);

      // Friendly label: "clarity" from "reviewer-clarity-claude"
      var label = name;
      if (role === "reviewer") {
        var parts = name.replace("reviewer-", "").split("-");
        label = parts[0]; // lens name
      } else if (role === "holdout") {
        label = "holdout";
      }

      var chip = el("div", { className: "agent-chip agent-chip-" + provider + " agent-role-" + role });
      var dot = el("span", { className: "agent-chip-dot agent-dot-" + (info.status || "running") });
      var labelEl = el("span", { className: "agent-chip-label" }, [document.createTextNode(label)]);
      var providerEl = el("span", { className: "agent-chip-provider" }, [document.createTextNode(provider)]);

      chip.appendChild(dot);
      chip.appendChild(labelEl);
      chip.appendChild(providerEl);
      chip.title = name;
      grid.appendChild(chip);
    }

    container.appendChild(grid);
  }

  function restoreActiveAgents(featureName) {
    if (!featureName) return;
    fetchJSON("/api/workflow/agents?feature=" + encodeURIComponent(featureName)).then(function (data) {
      if (!data || !data.agents) return;
      activeAgents = {};
      for (var i = 0; i < data.agents.length; i++) {
        var a = data.agents[i];
        activeAgents[a.name] = {
          provider: getAgentProvider(a.name),
          status: a.status || "running"
        };
      }
      renderActiveAgents();
    }).catch(function () {});
  }

  function onAgentDispatch(data) {
    addActivityEntry("Dispatching " + (data.agent || "?") + "...", "info", data.feature_name);
    var agentName = data.agent || "";
    if (agentName) {
      activeAgents[agentName] = {
        dispatched: data.timestamp || new Date().toISOString(),
        provider: getAgentProvider(agentName),
        status: "running"
      };
      renderActiveAgents();
    }
  }

  function onAgentComplete(data) {
    var agentName = data.agent || "?";
    if (data.success) {
      var msg = agentName + " completed";
      var details = [];
      if (data.duration_ms != null) details.push(data.duration_ms + "ms");
      if (data.cost_usd != null) details.push(formatCost(data.cost_usd));
      if (details.length > 0) msg += " (" + details.join(", ") + ")";
      addActivityEntry(msg, "success", data.feature_name);
    } else {
      addActivityEntry(agentName + " FAILED", "error", data.feature_name);
    }
    // Update status in active agents panel (keep visible briefly to show result).
    if (activeAgents[agentName]) {
      activeAgents[agentName].status = data.success ? "done" : "failed";
      renderActiveAgents();
      // Remove after 3 seconds so completed agents fade out.
      setTimeout(function () {
        delete activeAgents[agentName];
        renderActiveAgents();
      }, 3000);
    }
  }

  function onWorkflowStatus(data) {
    // Always refresh the full status list so all workflows badges stay current.
    refreshWorkflowStatusList();

    // Only update the detail panel if this event is for the selected workflow.
    if (eventMatchesSelectedFeature(data)) {
      updateWorkflowStatus(data);
    }
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
    var prefix = data.agent_name ? "[" + data.agent_name + "] " : "";
    var msg = prefix + "Tool: " + (data.tool_name || "?");
    if (data.duration_ms) msg += " (" + Math.round(data.duration_ms) + "ms)";
    if (!data.success) msg += " FAILED";
    addActivityEntry(msg, status, data.feature_name);
  }

  function onAgentAPIEvent(data) {
    var prefix = data.agent_name ? "[" + data.agent_name + "] " : "";
    var msg = prefix + "API: " + (data.model || "?");
    var details = [];
    if (data.duration_ms) details.push(Math.round(data.duration_ms) + "ms");
    if (data.cost_usd) details.push(formatCost(data.cost_usd));
    if (details.length > 0) msg += " (" + details.join(", ") + ")";
    addActivityEntry(msg, "info", data.feature_name);
  }

  // -----------------------------------------------------------------------
  // Persisted Metrics Restoration
  // -----------------------------------------------------------------------

  /**
   * Loads persisted OTEL metrics and events from SQLite via the HTTP API.
   * Called on page load and WebSocket reconnect to restore dashboard state
   * that would otherwise be lost on browser refresh.
   *
   * @param {string} featureName - workflow to restore metrics for
   * @param {boolean} restoreActivity - if true, also restore activity feed
   *   entries from persisted events (only on initial load / reconnect)
   */
  function restorePersistedMetrics(featureName, restoreActivity) {
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
      // but we add oldest first so newest ends up on top). Only on initial
      // load or WS reconnect — not on workflow switch.
      if (restoreActivity && data.events && data.events.length > 0) {
        var events = data.events.slice().reverse(); // oldest first
        for (var i = 0; i < events.length; i++) {
          var evt = events[i];
          if (evt.event_type === "tool") {
            var toolMsg = "Tool: " + (evt.tool_name || "?");
            if (evt.duration_ms) toolMsg += " (" + Math.round(evt.duration_ms) + "ms)";
            if (!evt.success) toolMsg += " FAILED";
            addActivityEntry(toolMsg, evt.success ? "success" : "error", featureName);
          } else if (evt.event_type === "api") {
            var apiMsg = "API: " + (evt.model || "?");
            var apiDetails = [];
            if (evt.duration_ms) apiDetails.push(Math.round(evt.duration_ms) + "ms");
            if (evt.cost_usd) apiDetails.push(formatCost(evt.cost_usd));
            if (apiDetails.length > 0) apiMsg += " (" + apiDetails.join(", ") + ")";
            addActivityEntry(apiMsg, "info", featureName);
          }
        }
      }
    }).catch(function (err) {
      console.warn("Failed to restore persisted metrics:", err);
    });
  }

  function pollWorkflowStatus() {
    fetchJSON("/api/workflow/status").then(function (data) {
      if (!data) return;

      // Handle array format: render the status list, then process
      // the selected (or first active) workflow for the detail panel.
      var statuses = Array.isArray(data) ? data : [data];
      renderWorkflowStatusList(statuses);

      // Find the status to display in the detail panel:
      // prefer selectedFeature, otherwise first non-idle workflow.
      var displayStatus = null;
      for (var i = 0; i < statuses.length; i++) {
        if (selectedFeature && statuses[i].feature_name === selectedFeature) {
          displayStatus = statuses[i];
          break;
        }
      }
      if (!displayStatus) {
        // Prefer running workflows over terminal ones.
        for (var j = 0; j < statuses.length; j++) {
          if (statuses[j].is_running) {
            displayStatus = statuses[j];
            break;
          }
        }
        if (!displayStatus) {
          for (var k = 0; k < statuses.length; k++) {
            if (statuses[k].state && statuses[k].state.toUpperCase() !== "IDLE") {
              displayStatus = statuses[k];
              break;
            }
          }
        }
      }

      // Legacy single-status handling for detail panel
      if (!displayStatus || !displayStatus.state) return;

      var state = displayStatus.state.toUpperCase();

      // Don't overwrite an active workflow display with an "idle" response.
      if (state === "IDLE" && workflowActive) return;

      if (state !== "IDLE") {
        updateWorkflowStatus(displayStatus);
      }

      // Restore active agents from server on page load / reconnect.
      // Do NOT restore for terminal states — they have no live agents.
      var terminalStates = ["COMPLETE", "FINALIZED", "ESCALATED", "CR_COMPLETE", "CR_ESCALATED", "ERROR"];
      var isTerminal = terminalStates.indexOf((displayStatus.state || "").toUpperCase()) !== -1;
      if (displayStatus.is_running && !isTerminal && Object.keys(activeAgents).length === 0) {
        restoreActiveAgents(displayStatus.feature_name);
      }

      // If all workflows are idle or terminal, stop the workflow poller.
      var anyActive = statuses.some(function (s) {
        var st = (s.state || "").toUpperCase();
        return st !== "IDLE" && st !== "COMPLETE" && st !== "FINALIZED" && st !== "ESCALATED" && st !== "CR_COMPLETE" && st !== "CR_ESCALATED";
      });
      if (!anyActive) {
        stopWorkflowPoller();
        return;
      }

      // If gate state and gate panel not already showing, show it.
      if (state.indexOf("HUMAN_GATE") !== -1 || state === "TASK_HUMAN_GATE" || state.indexOf("CR_HUMAN_GATE") !== -1) {
        var gatePanel = $(".gate-panel");
        if (!gatePanel) {
          var feature = displayStatus.feature_name;
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
          } else if (state === "TASK_HUMAN_GATE" && feature) {
            showTaskGatePanel({ gate_type: "task_human_gate", data: {}, task_id: feature });
          } else if ((state === "CR_HUMAN_GATE_SCOPE" || state === "CR_HUMAN_GATE_FIXES") && feature) {
            fetchJSON("/api/codereview/" + encodeURIComponent(feature) + "/status").then(function (crStatus) {
              if (!crStatus) return;
              if (state === "CR_HUMAN_GATE_SCOPE") {
                showCRScopeGatePanel(feature, crStatus);
              } else {
                showCRFixesGatePanel(feature, crStatus);
              }
            }).catch(function () {});
          } else if (state === "HUMAN_GATE_FINAL" && feature) {
            refreshGatePanelsForFeature(feature);
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
    // State is keyed by the stable AMB-W-NNN identifier carried in
    // data-amb-id. Previously this used the ordinal data-idx, which caused
    // stale drafts from an earlier drafter round to bleed into a new round
    // whose warnings had different IDs at the same ordinal positions.
    var state = { schema: "amb-id-v1" };
    var actions = {};
    $$(".amb-action", panel).forEach(function (sel) {
      var id = sel.dataset.ambId;
      if (id) actions[id] = sel.value;
    });
    state.actions = actions;
    var answers = {};
    $$(".amb-answer", panel).forEach(function (input) {
      var id = input.dataset.ambId;
      if (id) answers[id] = input.value;
    });
    state.answers = answers;
    var commentEl = $("#gate2-comment");
    state.comment = commentEl ? commentEl.value : "";
    return state;
  }

  function restoreGate2FormState(panel, saved) {
    if (!saved) return;
    // Only restore state saved by the current schema. Older state keyed by
    // ordinal idx (pre amb-id-v1) is silently dropped: it cannot be safely
    // mapped onto a new set of ambiguity warnings because the IDs may have
    // changed across drafter re-runs.
    if (saved.schema !== "amb-id-v1") return;

    if (saved.actions) {
      $$(".amb-action", panel).forEach(function (sel) {
        var id = sel.dataset.ambId;
        if (!id) return;
        var val = saved.actions[id];
        if (val) sel.value = val;
      });
    }
    if (saved.answers || saved.actions) {
      $$(".amb-answer", panel).forEach(function (textarea) {
        var id = textarea.dataset.ambId;
        if (!id) return;
        var answerRow = panel.querySelector(".amb-answer-row[data-amb-id='" + id + "']");
        var sel = panel.querySelector(".amb-action[data-amb-id='" + id + "']");
        var isAnswer = sel && sel.value === "answer";
        if (isAnswer && !gate2AnswerDisabled) {
          if (answerRow) answerRow.style.display = "";
          textarea.disabled = false;
        }
        if (saved.answers) {
          var val = saved.answers[id];
          if (val) textarea.value = val;
        }
      });
    }
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
      // Refresh the status list on connect/reconnect.
      refreshWorkflowStatusList();
      // On reconnect, restore persisted metrics that may have been missed.
      if (wasReconnect) {
        fetchJSON("/api/workflow/status").then(function (data) {
          if (!data) return;
          var statuses = Array.isArray(data) ? data : [data];
          statuses.forEach(function (status) {
            if (status && status.feature_name && status.state && status.state.toUpperCase() !== "IDLE") {
              if (!selectedFeature || status.feature_name === selectedFeature) {
                restorePersistedMetrics(status.feature_name, true);
              }
            }
          });
        }).catch(function () {});
        // Re-fetch Running Agents table to recover events missed during disconnect.
        loadRunningAgents();
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

  /**
   * Returns true if the event belongs to the currently selected workflow
   * (or if no workflow is selected, allowing all events through).
   */
  function eventMatchesSelectedFeature(data) {
    if (!selectedFeature) return true;
    if (!data || !data.feature_name) return true; // events without feature_name pass through
    return data.feature_name === selectedFeature;
  }

  function handleEvent(envelope) {
    // Keepalive ping — ignore silently.
    if (envelope.event === "ping") return;

    // Add ALL WebSocket events to the Messages tab log (unfiltered).
    addWsEventToMessages(envelope);

    var data = envelope.data || {};

    // Ensure feature_name from the envelope is available on the data object
    // so downstream handlers can access it consistently.
    if (envelope.feature_name && !data.feature_name) {
      data.feature_name = envelope.feature_name;
    }

    // Workflow status list updates always apply (all workflows).
    // state_transition also refreshes the list, so it runs unconditionally
    // for the list update, but detail panel updates are filtered below.
    if (envelope.event === "workflow_status") {
      onWorkflowStatus(data);
      return;
    }

    // state_transition: always refresh the workflow list for badge updates,
    // but only update the detail panel / activity if it matches the selection.
    if (envelope.event === "state_transition") {
      // Always refresh the status list so all workflow badges stay current.
      refreshWorkflowStatusList();

      // Set notification badge if transitioning to a gate state on an
      // unselected workflow.
      if (data.feature_name && data.feature_name !== selectedFeature) {
        var toState = (data.to || "").toUpperCase();
        if (toState === "HUMAN_GATE_1" || toState === "HUMAN_GATE_2" || toState === "HUMAN_GATE_FINAL" || toState === "CR_HUMAN_GATE_SCOPE" || toState === "CR_HUMAN_GATE_FIXES") {
          workflowBadges[data.feature_name] = true;
          renderWorkflowStatusList(allWorkflowStatuses);
        }
      }

      if (eventMatchesSelectedFeature(data)) {
        onStateTransition(data);
      }
      return;
    }

    // gate_request: set notification badge for unselected workflows before
    // the general filter drops the event.
    if (envelope.event === "gate_request") {
      if (data.feature_name && data.feature_name !== selectedFeature) {
        workflowBadges[data.feature_name] = true;
        renderWorkflowStatusList(allWorkflowStatuses);
      }
    }

    // Process tracking events apply globally (not filtered by selected workflow).
    if (envelope.event === "process_started") {
      onProcessStarted(data);
      return;
    }
    if (envelope.event === "process_ended") {
      onProcessEnded(data);
      return;
    }
    if (envelope.event === "process_lost") {
      onProcessLost(data);
      return;
    }

    // For all other events, only update UI panels if the event matches
    // the selected workflow (or no workflow is selected).
    if (!eventMatchesSelectedFeature(data)) return;

    switch (envelope.event) {
      case "spec_version":
        onSpecVersion(data);
        break;
      case "issue_update":
        onIssueUpdate(data);
        break;
      case "convergence_update":
        onConvergenceUpdate(data);
        break;
      case "gate_request":
        onGateRequest(data);
        break;
      case "gate_response":
        onGateResponse(data);
        break;
      case "circuit_breaker":
        onCircuitBreaker(data);
        break;
      case "agent_error":
        onAgentError(data);
        break;
      case "agent_dispatch":
        onAgentDispatch(data);
        break;
      case "agent_complete":
        onAgentComplete(data);
        break;
      case "agent_metrics":
        onAgentMetrics(data);
        break;
      case "agent_tool_event":
        onAgentToolEvent(data);
        break;
      case "agent_api_event":
        onAgentAPIEvent(data);
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
        if (tab === "tasks") loadTasks();
        if (tab === "convergence") loadConvergence();
        if (tab === "messages") startMessagesPolling();
        if (tab === "running-agents") loadRunningAgents();
        if (tab === "workspace") loadWorkspaceFiles();

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
    if (s === "COMPLETE") return "ws-finalized";
    if (s === "FINALIZED") return "ws-active";
    if (s === "ESCALATED" || s === "ERROR") return "ws-escalated";
    if (s.indexOf("HUMAN_GATE") !== -1 || s === "TASK_HUMAN_GATE") return "ws-gate";
    if (s === "UNKNOWN") return "ws-unknown";
    // Active states: INIT, DISCOVERY, DRAFTING, REVIEWING, REVISING, JUDGING, TASKIFY, TASK_REVIEW, etc.
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

        // Running indicator
        if (f.is_running) {
          nameRow.appendChild(el("span", { className: "workflow-running-badge" }, [
            el("span", { className: "wsi-running-dot" }),
            document.createTextNode("agent running")
          ]));
        } else {
          var fStateUp = (f.state || "").toUpperCase();
          var fIsTerminal = fStateUp === "COMPLETE" || fStateUp === "ESCALATED" || fStateUp === "ERROR" ||
            fStateUp === "CR_COMPLETE" || fStateUp === "CR_ESCALATED" ||
            fStateUp === "CD_COMPLETE" || fStateUp === "CD_ESCALATED" || fStateUp === "CD_ERROR";
          if (!fIsTerminal && fStateUp !== "IDLE" && fStateUp !== "UNKNOWN" && fStateUp !== "") {
            nameRow.appendChild(el("span", { className: "workflow-not-running-badge", textContent: "not running" }));
          }
        }

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

        // Source documents list (expandable).
        if (f.source_docs && f.source_docs.length > 0) {
          var docsDetails = el("details", { className: "workflow-source-docs" });
          docsDetails.appendChild(el("summary", { textContent: f.source_docs.length + " source docs" }));
          var docsList = el("ul", { className: "workflow-source-docs-list" });
          f.source_docs.forEach(function (doc) {
            docsList.appendChild(el("li", { textContent: doc }));
          });
          docsDetails.appendChild(docsList);
          info.appendChild(docsDetails);
        }

        card.appendChild(info);

        // Actions section
        var actions = el("div", { className: "workflow-actions" });

        var stateUpper = (f.state || "").toUpperCase();
        var wfType = f.workflow_type || "spec";
        var isTerminal = f.is_terminal;
        var isPaused = f.is_paused;
        var isGate = stateUpper.indexOf("HUMAN_GATE") !== -1 || stateUpper === "TASK_HUMAN_GATE";
        var isActive = !isTerminal && !isGate && !isPaused && stateUpper !== "UNKNOWN";

        if ((isTerminal || isPaused) && stateUpper !== "UNKNOWN") {
          // Resume button — continues from where it left off
          if (stateUpper === "ESCALATED" || stateUpper === "ERROR" || isPaused) {
            var termResumeBtn = el("button", {
              className: "btn btn-success btn-sm",
              textContent: "Resume"
            });
            termResumeBtn.addEventListener("click", (function (featureName) {
              return function () {
                termResumeBtn.disabled = true;
                termResumeBtn.textContent = "Loading...";
                // Fetch resume options so we can offer the user a choice
                // (skip_to_gate / replay_merge / restart_fresh) instead of
                // blindly re-running the current stage.
                fetchJSON("/api/workflow/resume-options?feature_name=" + encodeURIComponent(featureName))
                  .then(function (opts) {
                    termResumeBtn.textContent = "Resume";
                    termResumeBtn.disabled = false;
                    showResumeChoiceModal(featureName, opts, function (mode) {
                      termResumeBtn.disabled = true;
                      termResumeBtn.textContent = "Resuming...";
                      fetchJSON("/api/workflow/resume", {
                        method: "POST",
                        headers: { "Content-Type": "application/json" },
                        body: JSON.stringify({ feature_name: featureName, mode: mode })
                      }).then(function (data) {
                        updateWorkflowStatus({
                          state: data.resume_state || "REVIEWING",
                          feature_name: featureName,
                          round: 1,
                          cost_usd: 0,
                          wall_clock_seconds: 0,
                          agent_invocations: 0
                        });
                        var msg = "Workflow resumed: " + featureName + " from " + (data.resume_state || "?") + " (mode: " + (data.mode || "auto") + ")";
                        if (data.replay_message) msg += " — " + data.replay_message;
                        addActivityEntry(msg, "info");
                        startWorkflowPoller();
                        loadFeatureList();
                      }).catch(function (err) {
                        alert("Resume failed: " + err.message);
                        termResumeBtn.disabled = false;
                        termResumeBtn.textContent = "Resume";
                      });
                    });
                  })
                  .catch(function (err) {
                    alert("Could not load resume options: " + err.message);
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
            className: "btn btn-purple btn-sm",
            textContent: "Resume"
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
                } else if (stateStr.indexOf("HUMAN_GATE_FINAL") !== -1) {
                  refreshGatePanelsForFeature(featureName);
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

        // Replay Merge button — re-runs merge/combine step using files on disk
        var replayPhase = null;
        var replayLabel = null;
        if (stateUpper === "HUMAN_GATE_1" || (isTerminal && stateUpper !== "UNKNOWN")) {
          replayPhase = "discovery_merge";
          replayLabel = "Replay Discovery Merge";
        } else if (stateUpper === "HUMAN_GATE_2") {
          replayPhase = "drafting_combine";
          replayLabel = "Replay Draft Combine";
        } else if (stateUpper === "REVIEWING" || stateUpper === "REVISING" || stateUpper === "JUDGING" || stateUpper === "HUMAN_GATE_FINAL") {
          replayPhase = "review_merge";
          replayLabel = "Replay Review Merge";
        } else if (stateUpper === "TASK_REVIEW" || stateUpper === "TASK_REVISION" || stateUpper === "TASK_HUMAN_GATE") {
          replayPhase = "task_review_merge";
          replayLabel = "Replay Task Review Merge";
        }
        if (replayPhase && !isActive) {
          var replayBtn = el("button", {
            className: "btn btn-sm",
            textContent: replayLabel,
            style: "background:#e2e3f1;color:#383d6e;border-color:#c5c7e0;"
          });
          replayBtn.addEventListener("click", (function (featureName, phase, label) {
            return function () {
              if (!confirm("Re-run " + label + " for \"" + featureName + "\" using existing files on disk?")) return;
              replayBtn.disabled = true;
              replayBtn.textContent = "Replaying...";
              fetchJSON("/api/workflow/replay", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ feature_name: featureName, phase: phase })
              }).then(function (data) {
                addActivityEntry("Replay complete: " + (data.message || label) + " for " + featureName, "info");
                loadFeatureList();
                replayBtn.disabled = false;
                replayBtn.textContent = label;
              }).catch(function (err) {
                alert("Replay failed: " + err.message);
                replayBtn.disabled = false;
                replayBtn.textContent = label;
              });
            };
          })(f.feature_name, replayPhase, replayLabel));
          actions.appendChild(replayBtn);
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
            className: "btn btn-warning btn-sm",
            textContent: "Restart"
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

        // Finalize button — force-finish a stuck workflow
        if (!isTerminal && stateUpper !== "UNKNOWN") {
          var finalizeBtn = el("button", {
            className: "btn btn-success btn-sm",
            textContent: "Finalize"
          });
          finalizeBtn.addEventListener("click", (function (featureName) {
            return function () {
              if (!confirm("Force-finalize workflow \"" + featureName + "\"? This will mark it as done and stop any running agents.")) return;
              finalizeBtn.disabled = true;
              finalizeBtn.textContent = "Finalizing...";
              fetchJSON("/api/workflow/finalize", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ feature_name: featureName })
              }).then(function () {
                addActivityEntry("Workflow finalized: " + featureName, "info");
                loadFeatureList();
                refreshWorkflowStatusList();
              }).catch(function (err) {
                alert("Finalize failed: " + err.message);
                finalizeBtn.disabled = false;
                finalizeBtn.textContent = "Finalize";
              });
            };
          })(f.feature_name));
          actions.appendChild(finalizeBtn);
        }

        // Delete button (always shown for features with content)
        var deleteBtn = el("button", {
          className: "btn btn-danger btn-sm",
          textContent: "Delete"
        });
        deleteBtn.addEventListener("click", (function (featureName, workflowType) {
          return function () {
            if (!confirm("Delete all files for '" + featureName + "'? This cannot be undone.")) return;
            runWorkflowReset(workflowType, featureName).then(function () {
              // If the deleted workflow is currently displayed, clear the status panel.
              var displayed = ($("#status-feature").textContent || "").trim();
              if (displayed === featureName) {
                updateWorkflowStatus({
                  state: "IDLE",
                  feature_name: "-",
                  round: 0,
                  cost_usd: 0,
                  wall_clock_seconds: 0,
                  agent_invocations: 0
                });
                // Clear activity feed.
                clearChildren($("#status-activity"));
              }
              loadFeatureList();
            }).catch(function (err) {
              alert("Delete failed: " + err.message);
            });
          };
        })(f.feature_name, wfType));
        actions.appendChild(deleteBtn);

        // Rewind controls — stage dropdown + rewind button.
        // Available for any workflow that has progressed past INIT.
        if (stateUpper !== "UNKNOWN" && stateUpper !== "INIT") {
          var rewindRow = el("div", { className: "workflow-rewind", style: "display:flex;gap:6px;align-items:center;margin-top:6px;" });

          var stageSelect = el("select", { className: "rewind-select", style: "font-size:12px;padding:2px 4px;" });
          var stages = ["DISCOVERY", "DRAFTING", "REVIEWING", "REVISING", "JUDGING", "TASKIFY", "TASK_REVIEW"];
          stages.forEach(function (s) {
            var opt = el("option", { value: s, textContent: s });
            stageSelect.appendChild(opt);
          });
          // Default to the current state if it's rewindable, otherwise first option.
          if (stages.indexOf(stateUpper) !== -1) {
            stageSelect.value = stateUpper;
          }
          rewindRow.appendChild(el("span", { textContent: "Rewind to:", style: "font-size:12px;color:#666;" }));
          rewindRow.appendChild(stageSelect);

          var roundInput = el("input", {
            type: "number",
            min: "1",
            value: String(f.round || 1),
            style: "width:50px;font-size:12px;padding:2px 4px;",
            title: "Round number"
          });
          rewindRow.appendChild(roundInput);

          var rewindBtn = el("button", {
            className: "btn btn-warning btn-sm",
            textContent: "Rewind"
          });
          rewindBtn.addEventListener("click", (function (featureName, selectEl, roundEl) {
            return function () {
              var targetState = selectEl.value;
              var round = parseInt(roundEl.value, 10) || 1;
              if (!confirm("Rewind '" + featureName + "' to " + targetState + " round " + round + "? Artefacts will be preserved but the workflow will re-run from this stage.")) return;
              rewindBtn.disabled = true;
              rewindBtn.textContent = "Rewinding...";
              fetchJSON("/api/workflow/rewind", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ feature_name: featureName, target_state: targetState, round: round })
              }).then(function (data) {
                addActivityEntry("Workflow rewound: " + featureName + " to " + targetState + " round " + round + " (artefacts preserved)", "info");
                // Clear status if this workflow is displayed.
                var displayed = ($("#status-feature").textContent || "").trim();
                if (displayed === featureName) {
                  updateWorkflowStatus({
                    state: targetState,
                    feature_name: featureName,
                    round: round,
                    cost_usd: 0,
                    wall_clock_seconds: 0,
                    agent_invocations: 0
                  });
                }
                loadFeatureList();
              }).catch(function (err) {
                alert("Rewind failed: " + err.message);
              }).finally(function () {
                rewindBtn.disabled = false;
                rewindBtn.textContent = "Rewind";
              });
            };
          })(f.feature_name, stageSelect, roundInput));
          rewindRow.appendChild(rewindBtn);

          actions.appendChild(rewindRow);
        }

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
    var activeWfType = "spec";
    var workspaceDirGroup = $("#goal-workspace-dir-group");

    function updateWorkspaceDirVisibility() {
      if (!workspaceDirGroup) return;
      workspaceDirGroup.style.display = activeWfType === "spec" ? "block" : "none";
    }

    // Workflow type tab switching
    $$(".workflow-type-tab").forEach(function (btn) {
      btn.addEventListener("click", function () {
        $$(".workflow-type-tab").forEach(function (b) { b.classList.remove("active"); });
        btn.classList.add("active");
        activeWfType = btn.dataset.wfType;

        var specFields = $("#spec-fields");
        var crFields = $("#cr-fields");
        var cdFields = $("#cd-fields");

        // Hide all type-specific fields first.
        specFields.style.display = "none";
        crFields.style.display = "none";
        cdFields.style.display = "none";
        $("#goal-title").removeAttribute("required");
        $("#goal-description").removeAttribute("required");
        $("#cr-code-path").removeAttribute("required");
        $("#cd-code-path").removeAttribute("required");

        if (activeWfType === "codereview") {
          crFields.style.display = "block";
          $("#cr-code-path").setAttribute("required", "required");
        } else if (activeWfType === "codedoc") {
          cdFields.style.display = "block";
          $("#cd-code-path").setAttribute("required", "required");
        } else {
          specFields.style.display = "block";
          $("#goal-title").setAttribute("required", "required");
          $("#goal-description").setAttribute("required", "required");
        }
        updateWorkspaceDirVisibility();
      });
    });
    updateWorkspaceDirVisibility();

    // Select all / deselect all for document picker
    $("#doc-select-all").addEventListener("click", function () {
      $$("#doc-picker input[type=checkbox]").forEach(function (cb) { cb.checked = true; });
    });
    $("#doc-deselect-all").addEventListener("click", function () {
      $$("#doc-picker input[type=checkbox]").forEach(function (cb) { cb.checked = false; });
    });

    // Refresh doc picker when the start-workflow section is opened
    $("#new-workflow-section").addEventListener("toggle", function () {
      if (this.open) renderDocPicker();
    });

    form.addEventListener("submit", function (e) {
      e.preventDefault();

      var featureName = $("#goal-feature-name").value.trim();
      var workspaceDir = $("#goal-workspace-dir").value.trim();
      var submitBtn = $("#goal-submit");
      submitBtn.disabled = true;
      submitBtn.textContent = "Starting...";

      var url, payload;

      if (activeWfType === "codereview") {
        url = "/api/codereview/start";
        payload = {
          code_path: $("#cr-code-path").value.trim(),
          feature_name: featureName,
          spec_path: $("#cr-spec-path").value.trim() || undefined,
          task_list_path: $("#cr-task-list-path").value.trim() || undefined
        };
      } else if (activeWfType === "codedoc") {
        url = "/api/codedoc/start";
        payload = {
          code_path: $("#cd-code-path").value.trim(),
          feature_name: featureName,
          mode: $("#cd-mode").value,
          description: $("#cd-description").value.trim() || undefined
        };
      } else {
        url = "/api/workflow/start";
        payload = {
          title: $("#goal-title").value.trim(),
          feature_name: featureName,
          description: $("#goal-description").value.trim()
        };
        if (workspaceDir) payload.workspace_dir = workspaceDir;
        var codePath = $("#goal-code-path").value.trim();
        if (codePath) payload.code_path = codePath;

        // Collect selected source documents
        var selectedDocs = [];
        $$("#doc-picker input[type=checkbox]:checked").forEach(function (cb) {
          selectedDocs.push(cb.value);
        });
        if (selectedDocs.length > 0) {
          payload.source_doc_paths = selectedDocs;
        }
      }

      fetchJSON(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      }).then(function (data) {
        var initState = activeWfType === "codereview" ? (data.state || "CR_INIT") :
                        activeWfType === "codedoc" ? (data.state || "CD_INIT") : (data.state || "INIT");
        updateWorkflowStatus({
          state: initState,
          feature_name: data.feature_name || featureName,
          round: data.round || 1,
          cost_usd: 0,
          wall_clock_seconds: 0,
          agent_invocations: 0
        });
        var typeLabel = activeWfType === "codereview" ? "Code review" :
                        activeWfType === "codedoc" ? "Code doc" : "Workflow";
        addActivityEntry(typeLabel + " started: " + (data.feature_name || featureName), "info");
        // Auto-select the newly started workflow so events route to the detail panel.
        selectWorkflow(data.feature_name || featureName);
        startWorkflowPoller();
        form.reset();
        // Restore the active tab visual state after reset
        $$(".workflow-type-tab").forEach(function (b) { b.classList.remove("active"); });
        $$(".workflow-type-tab").forEach(function (b) {
          if (b.dataset.wfType === "spec") b.classList.add("active");
        });
        activeWfType = "spec";
        $("#spec-fields").style.display = "block";
        $("#cr-fields").style.display = "none";
        $("#cd-fields").style.display = "none";
        updateWorkspaceDirVisibility();
        // Collapse the new workflow section and refresh the list
        var details = $("#new-workflow-section");
        if (details) details.open = false;
        loadFeatureList();
      }).catch(function (err) {
        var msg = err.message || "";
        if (msg.indexOf("409") !== -1) {
          alert("A workflow is already in progress for this feature.");
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
      // Also refresh the document picker so new uploads appear as checkable items
      renderDocPicker();
    }).catch(function () {});
  }

  // -----------------------------------------------------------------------
  // Controls Tab — Document Picker
  // -----------------------------------------------------------------------

  function renderDocPicker() {
    fetchJSON("/api/workspace/uploads").then(function (files) {
      var picker = $("#doc-picker");
      clearChildren(picker);

      if (!files || files.length === 0) {
        picker.appendChild(el("p", { className: "doc-picker-empty", textContent: "No documents uploaded yet. Upload documents below." }));
        return;
      }

      files.forEach(function (f) {
        var cbId = "doc-" + f.name.replace(/[^a-zA-Z0-9]/g, "-");
        var checkbox = el("input", { type: "checkbox", id: cbId, value: f.name });
        checkbox.checked = true;

        var div = el("div", { className: "doc-picker-item" }, [
          checkbox,
          el("label", { htmlFor: cbId, textContent: f.name }),
          el("span", { className: "doc-size", textContent: formatBytes(f.size) })
        ]);
        picker.appendChild(div);
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
    var url = "/api/spec/versions";
    if (selectedFeature) url += "?feature=" + encodeURIComponent(selectedFeature);

    fetchJSON(url).then(function (versions) {
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
    if (selectedFeature) {
      url += (url.indexOf("?") === -1 ? "?" : "&") + "feature=" + encodeURIComponent(selectedFeature);
    }

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
    var url = "/api/spec/diff/" + a + "/" + b;
    if (selectedFeature) url += "?feature=" + encodeURIComponent(selectedFeature);
    fetchJSON(url).then(function (data) {
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
    if (selectedFeature) params.set("feature", selectedFeature);

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
  // Tasks Tab
  // -----------------------------------------------------------------------

  function loadTasks() {
    var url = "/api/spec/tasks";
    if (selectedFeature) url += "?feature=" + encodeURIComponent(selectedFeature);
    fetchJSON(url).then(function (resp) {
      taskCache = (resp && resp.tasks) ? resp.tasks : [];
      renderTasks(taskCache);
      updateTaskSummary(taskCache);
    }).catch(function () {
      taskCache = [];
      renderTasks([]);
      updateTaskSummary([]);
    });
  }

  function renderTasks(tasks) {
    var tbody = $("#task-table tbody");
    clearChildren(tbody);

    tasks.forEach(function (task) {
      var tr = el("tr", { className: "expandable" });
      var deps = buildDepsCell(task, tasks);
      tr.innerHTML =
        "<td><code>" + escapeHtml(task.task_id || "-") + "</code></td>" +
        "<td>" + escapeHtml(task.task_name || "-") + "</td>" +
        "<td>" + priorityBadge(task.priority) + "</td>" +
        "<td>" + estimateBadge(task.estimate) + "</td>" +
        "<td class='task-deps-cell'>" + deps + "</td>";

      tr.addEventListener("click", function (e) {
        // Chip clicks are handled separately — don't toggle detail for them.
        if (e.target.classList.contains("task-dep-chip")) return;
        var next = tr.nextElementSibling;
        if (next && next.classList.contains("task-detail")) {
          next.remove();
        } else {
          var detail = buildTaskDetail(task, tasks);
          tr.parentNode.insertBefore(detail, tr.nextSibling);
        }
      });

      tbody.appendChild(tr);
    });

    // Wire up dep-chip click navigation after all rows are rendered.
    $("#task-table").querySelectorAll(".task-dep-chip[data-task-id]").forEach(function (chip) {
      chip.addEventListener("click", function (e) {
        e.stopPropagation();
        scrollToTask(chip.dataset.taskId);
      });
    });
  }

  function buildDepsCell(task, allTasks) {
    var deps = task.depends_on;
    if (!deps || (typeof deps === "object" && deps.status === "N/A")) return "<span class='task-na'>N/A</span>";
    if (!Array.isArray(deps) || deps.length === 0) return "<span class='task-na'>none</span>";
    return deps.map(function (id) {
      return "<span class='task-dep-chip' data-task-id='" + escapeHtml(id) + "'>" + escapeHtml(id) + "</span>";
    }).join(" ");
  }

  function buildTaskDetail(task, allTasks) {
    var tr = el("tr", { className: "task-detail" });
    var td = el("td", { colSpan: "5" });

    var html = '<div class="task-detail-content">';

    // Goal
    if (task.goal) {
      html += '<div class="task-detail-section"><dt>Goal</dt><dd>' + escapeHtml(task.goal) + '</dd></div>';
    }

    // Acceptance criteria
    if (task.acceptance && task.acceptance.length > 0) {
      html += '<div class="task-detail-section"><dt>Acceptance Criteria</dt><dd><ul class="task-acceptance-list">';
      task.acceptance.forEach(function (c) {
        html += '<li>' + escapeHtml(c) + '</li>';
      });
      html += '</ul></dd></div>';
    }

    // Inputs/Outputs
    if (task.inputs && task.inputs.length > 0) {
      html += '<div class="task-detail-section"><dt>Inputs</dt><dd>';
      html += buildIOTable(task.inputs);
      html += '</dd></div>';
    }
    if (task.outputs && task.outputs.length > 0) {
      html += '<div class="task-detail-section"><dt>Outputs</dt><dd>';
      html += buildIOTable(task.outputs);
      html += '</dd></div>';
    }

    // Files scope
    if (task.files_scope && task.files_scope.length > 0) {
      html += '<div class="task-detail-section"><dt>Files Scope</dt><dd class="task-files">';
      task.files_scope.forEach(function (f) {
        html += '<code class="task-file">' + escapeHtml(f) + '</code>';
      });
      html += '</dd></div>';
    }

    // Depends on (full IDs as chips)
    var deps = task.depends_on;
    if (Array.isArray(deps) && deps.length > 0) {
      html += '<div class="task-detail-section"><dt>Depends On</dt><dd class="task-dep-chips">';
      deps.forEach(function (id) {
        html += '<span class="task-dep-chip task-dep-chip-detail" data-task-id="' + escapeHtml(id) + '">' + escapeHtml(id) + '</span>';
      });
      html += '</dd></div>';
    }

    // Blocked by (reverse deps)
    var blockedBy = allTasks.filter(function (t) {
      return Array.isArray(t.depends_on) && t.depends_on.indexOf(task.task_id) !== -1;
    });
    if (blockedBy.length > 0) {
      html += '<div class="task-detail-section"><dt>Blocked By (depended on by)</dt><dd class="task-dep-chips">';
      blockedBy.forEach(function (t) {
        html += '<span class="task-dep-chip task-dep-chip-detail" data-task-id="' + escapeHtml(t.task_id) + '">' + escapeHtml(t.task_id) + '</span>';
      });
      html += '</dd></div>';
    }

    // Constraints
    if (task.constraints && task.constraints.length > 0) {
      html += '<div class="task-detail-section"><dt>Constraints</dt><dd><ul class="task-acceptance-list">';
      task.constraints.forEach(function (c) {
        html += '<li>' + escapeHtml(c) + '</li>';
      });
      html += '</ul></dd></div>';
    }

    // Notes
    if (task.notes) {
      html += '<div class="task-detail-section"><dt>Notes</dt><dd>' + escapeHtml(task.notes) + '</dd></div>';
    }

    html += '</div>';
    td.innerHTML = html;
    tr.appendChild(td);

    // Wire up detail dep-chip navigation.
    tr.querySelectorAll(".task-dep-chip-detail[data-task-id]").forEach(function (chip) {
      chip.addEventListener("click", function (e) {
        e.stopPropagation();
        scrollToTask(chip.dataset.taskId);
      });
    });

    return tr;
  }

  function buildIOTable(items) {
    var html = '<table class="table task-io-table"><thead><tr><th>Name</th><th>Type</th><th>Constraints</th><th>Source/Dest</th></tr></thead><tbody>';
    items.forEach(function (item) {
      html += '<tr>' +
        '<td>' + escapeHtml(item.name || "-") + '</td>' +
        '<td>' + escapeHtml(item.type || "-") + '</td>' +
        '<td>' + escapeHtml(item.constraints || "-") + '</td>' +
        '<td>' + escapeHtml(item.source || item.destination || "-") + '</td>' +
        '</tr>';
    });
    html += '</tbody></table>';
    return html;
  }

  function updateTaskSummary(tasks) {
    var counts = { total: tasks.length, critical: 0, high: 0, medium: 0, low: 0 };
    tasks.forEach(function (t) {
      var p = (t.priority || "").toLowerCase();
      if (counts[p] !== undefined) counts[p]++;
    });
    $("#stat-task-total").textContent = counts.total;
    $("#stat-task-critical").textContent = counts.critical;
    $("#stat-task-high").textContent = counts.high;
    $("#stat-task-medium").textContent = counts.medium;
    $("#stat-task-low").textContent = counts.low;
  }

  function priorityBadge(priority) {
    var p = (priority || "").toLowerCase();
    var cls = {
      critical: "badge-critical",
      high:     "badge-major",
      medium:   "badge-minor",
      low:      "badge-observation"
    }[p] || "";
    return '<span class="badge ' + cls + '">' + escapeHtml(priority || "?") + '</span>';
  }

  function estimateBadge(estimate) {
    var e = (estimate || "").toLowerCase();
    var cls = {
      trivial: "badge-observation",
      small:   "badge-minor",
      medium:  "badge-minor",
      large:   "badge-major",
      unknown: ""
    }[e] || "";
    return '<span class="badge ' + cls + '">' + escapeHtml(estimate || "?") + '</span>';
  }

  function scrollToTask(taskId) {
    var tbody = $("#task-table tbody");
    var rows = tbody.querySelectorAll("tr.expandable");
    for (var i = 0; i < rows.length; i++) {
      var codeEl = rows[i].querySelector("td:first-child code");
      if (codeEl && codeEl.textContent === taskId) {
        rows[i].scrollIntoView({ behavior: "smooth", block: "center" });
        rows[i].classList.add("task-row-highlight");
        setTimeout(function (row) { row.classList.remove("task-row-highlight"); }, 1500, rows[i]);
        // If detail is not already open, open it.
        var next = rows[i].nextElementSibling;
        if (!next || !next.classList.contains("task-detail")) {
          rows[i].click();
        }
        return;
      }
    }
  }

  // -----------------------------------------------------------------------
  // Convergence Tab
  // -----------------------------------------------------------------------

  function loadConvergence() {
    var url = "/api/spec/convergence";
    if (selectedFeature) url += "?feature=" + encodeURIComponent(selectedFeature);

    fetchJSON(url).then(function (data) {
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
    var feature = data.feature_name || selectedFeature;
    if (data.gate_type === "requirements_confirmation") {
      // Fetch full discovery data from API instead of using sparse event payload.
      Promise.all([
        fetchJSON("/api/workspace/features/" + encodeURIComponent(feature) + "/discovery").catch(function () { return null; }),
        fetchJSON("/api/workspace/features/" + encodeURIComponent(feature) + "/files/gate1-corrections.json").catch(function () { return null; })
      ]).then(function (results) {
        if (results[0]) {
          showGate1Panel({ gate_type: "requirements_confirmation", data: results[0], task_id: feature }, results[1]);
        } else {
          showGate1Panel(data);
        }
      });
    } else if (data.gate_type === "ambiguity_resolution") {
      fetchJSON("/api/workspace/features/" + encodeURIComponent(feature) + "/files/drafter-output.json").then(function (drafter) {
        if (drafter) {
          showGate2Panel({ gate_type: "ambiguity_resolution", data: drafter, task_id: feature });
        } else {
          showGate2Panel(data);
        }
      }).catch(function () {
        showGate2Panel(data);
      });
    } else if (data.gate_type === "task_human_gate") {
      showTaskGatePanel(data);
    } else if (data.gate_type === "final_review") {
      refreshGatePanelsForFeature(feature);
    }
  }

  function onGateResponse(data) {
    addActivityEntry(
      "Gate response: " + (data.action || "unknown") + " — " + (data.detail || ""),
      data.action === "cancel" ? "error" : "success",
      data.feature_name
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

    // Assumptions — every assumption gets an answer field regardless of confidence
    if (discovery.assumptions && discovery.assumptions.length > 0) {
      var aHtml = "<ul>";
      discovery.assumptions.forEach(function (a, idx) {
        aHtml += "<li><strong>Assumption:</strong> " + escapeHtml(a.assumption) + " (confidence: " + escapeHtml(a.confidence) + ")";
        if (a.question_for_user) {
          aHtml += '<br><em>Question: ' + escapeHtml(a.question_for_user) + "</em>";
        }
        aHtml += '<br><textarea class="gate-assumption-answer" data-assumption-idx="' + idx + '" placeholder="Correct this assumption or leave blank to accept..."></textarea>';
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

  // --- Code Review Gate: Scope Confirmation ---

  function showCRScopeGatePanel(featureName, data) {
    var container = $("#gate-panels");
    clearChildren(container);

    var panel = el("div", { className: "gate-panel" });
    var header = '<h3><span class="gate-badge">CR Gate</span> Scope Confirmation</h3>';
    var content = "";

    content += buildGateSection("Feature", featureName);
    content += buildGateSection("State", data.state || "-");
    if (data.code_path) content += buildGateSection("Code Path", data.code_path);
    if (data.spec_path) content += buildGateSection("Spec Path", data.spec_path);
    if (data.round != null) content += buildGateSection("Round", String(data.round));

    content += '<div class="gate-section">' +
      '<div class="gate-section-label">Comment (optional)</div>' +
      '<textarea id="cr-gate-comment" class="gate-textarea" rows="3" placeholder="Add a comment..."></textarea>' +
      '</div>';

    content += '<div class="gate-actions">' +
      '<button id="cr-scope-confirm" class="btn btn-success">Confirm</button>' +
      '<button id="cr-scope-cancel" class="btn btn-danger">Cancel</button>' +
      '</div>';

    panel.innerHTML = header + content;
    container.appendChild(panel);

    $("#cr-scope-confirm").addEventListener("click", function () {
      submitCRGate(featureName, "confirm", $("#cr-gate-comment").value.trim());
    });
    $("#cr-scope-cancel").addEventListener("click", function () {
      submitCRGate(featureName, "cancel", $("#cr-gate-comment").value.trim());
    });
  }

  // --- Code Review Gate: Fixes Review ---

  function showCRFixesGatePanel(featureName, data) {
    var container = $("#gate-panels");
    clearChildren(container);

    var panel = el("div", { className: "gate-panel" });
    var header = '<h3><span class="gate-badge">CR Gate</span> Fixes Review</h3>';
    var content = "";

    content += buildGateSection("Feature", featureName);
    content += buildGateSection("State", data.state || "-");
    if (data.round != null) content += buildGateSection("Round", String(data.round));

    if (data.findings_summary) {
      var summary = data.findings_summary;
      var summaryHtml = "<ul>";
      if (summary.total != null) summaryHtml += "<li>Total findings: " + summary.total + "</li>";
      if (summary.critical != null) summaryHtml += "<li>Critical: " + summary.critical + "</li>";
      if (summary.major != null) summaryHtml += "<li>Major: " + summary.major + "</li>";
      if (summary.minor != null) summaryHtml += "<li>Minor: " + summary.minor + "</li>";
      if (summary.fixed != null) summaryHtml += "<li>Fixed: " + summary.fixed + "</li>";
      summaryHtml += "</ul>";
      content += buildGateSectionHtml("Findings Summary", summaryHtml);
    }

    content += '<div class="gate-section">' +
      '<div class="gate-section-label">Comment (optional)</div>' +
      '<textarea id="cr-gate-comment" class="gate-textarea" rows="3" placeholder="Add a comment..."></textarea>' +
      '</div>';

    content += '<div class="gate-actions">' +
      '<button id="cr-fixes-accept" class="btn btn-success">Accept</button>' +
      '<button id="cr-fixes-rereview" class="btn btn-primary">Re-review</button>' +
      '<button id="cr-fixes-escalate" class="btn btn-danger">Escalate</button>' +
      '</div>';

    panel.innerHTML = header + content;
    container.appendChild(panel);

    $("#cr-fixes-accept").addEventListener("click", function () {
      submitCRGate(featureName, "accept", $("#cr-gate-comment").value.trim());
    });
    $("#cr-fixes-rereview").addEventListener("click", function () {
      submitCRGate(featureName, "re-review", $("#cr-gate-comment").value.trim());
    });
    $("#cr-fixes-escalate").addEventListener("click", function () {
      submitCRGate(featureName, "escalate", $("#cr-gate-comment").value.trim());
    });
  }

  function submitCRGate(featureName, action, comment) {
    var payload = { action: action };
    if (comment) payload.comment = comment;

    fetchJSON("/api/codereview/" + encodeURIComponent(featureName) + "/gate", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload)
    }).then(function (resp) {
      addActivityEntry("CR gate " + action + ": " + featureName, "info");
      clearChildren($("#gate-panels"));
      refreshWorkflowStatusList();
    }).catch(function (err) {
      alert("Gate action failed: " + (err.message || err));
    });
  }

  // --- Codedoc Gate: Scope Confirmation ---

  function showCDScopeGatePanel(featureName, data) {
    var container = $("#gate-panels");
    clearChildren(container);

    var panel = el("div", { className: "gate-panel" });
    var header = '<h3><span class="gate-badge cd-gate-badge">CD Gate</span> Scope Confirmation</h3>';
    var content = "";

    content += buildGateSection("Feature", featureName);
    content += buildGateSection("State", data.state || "-");
    content += buildGateSection("Mode", data.mode || "full");
    if (data.round != null) content += buildGateSection("Round", String(data.round));

    content += '<div class="gate-section">' +
      '<div class="gate-section-label">Comment (optional)</div>' +
      '<textarea id="cd-gate-comment" class="gate-textarea" rows="3" placeholder="Add a comment..."></textarea>' +
      '</div>';

    content += '<div class="gate-actions">' +
      '<button id="cd-scope-confirm" class="btn btn-success">Confirm</button>' +
      '<button id="cd-scope-correct" class="btn btn-primary">Correct</button>' +
      '<button id="cd-scope-cancel" class="btn btn-danger">Cancel</button>' +
      '</div>';

    panel.innerHTML = header + content;
    container.appendChild(panel);

    $("#cd-scope-confirm").addEventListener("click", function () {
      submitCDGate(featureName, "confirm");
    });
    $("#cd-scope-correct").addEventListener("click", function () {
      submitCDGate(featureName, "correct");
    });
    $("#cd-scope-cancel").addEventListener("click", function () {
      submitCDGate(featureName, "cancel");
    });
  }

  // --- Codedoc Gate: Draft Review ---

  function showCDDraftGatePanel(featureName, data) {
    var container = $("#gate-panels");
    clearChildren(container);

    var panel = el("div", { className: "gate-panel" });
    var header = '<h3><span class="gate-badge cd-gate-badge">CD Gate</span> Draft Review</h3>';
    var content = "";

    content += buildGateSection("Feature", featureName);
    content += buildGateSection("State", data.state || "-");
    content += buildGateSection("Mode", data.mode || "full");
    if (data.round != null) content += buildGateSection("Round", String(data.round));

    content += '<div class="gate-section">' +
      '<div class="gate-section-label">Comment (optional)</div>' +
      '<textarea id="cd-gate-comment" class="gate-textarea" rows="3" placeholder="Add a comment..."></textarea>' +
      '</div>';

    content += '<div class="gate-actions">' +
      '<button id="cd-draft-approve" class="btn btn-success">Approve</button>' +
      '<button id="cd-draft-redraft" class="btn btn-primary">Redraft</button>' +
      '<button id="cd-draft-cancel" class="btn btn-danger">Cancel</button>' +
      '</div>';

    panel.innerHTML = header + content;
    container.appendChild(panel);

    $("#cd-draft-approve").addEventListener("click", function () {
      submitCDGate(featureName, "approve");
    });
    $("#cd-draft-redraft").addEventListener("click", function () {
      submitCDGate(featureName, "redraft");
    });
    $("#cd-draft-cancel").addEventListener("click", function () {
      submitCDGate(featureName, "cancel");
    });
  }

  // --- Codedoc Gate: Final Review ---

  function showCDFinalGatePanel(featureName, data) {
    var container = $("#gate-panels");
    clearChildren(container);

    var panel = el("div", { className: "gate-panel" });
    var header = '<h3><span class="gate-badge cd-gate-badge">CD Gate</span> Final Review</h3>';
    var content = "";

    content += buildGateSection("Feature", featureName);
    content += buildGateSection("State", data.state || "-");
    if (data.round != null) content += buildGateSection("Round", String(data.round));
    if (data.had_critical_findings) {
      content += buildGateSectionHtml("Findings", '<span class="text-danger">Unresolved CRITICAL/MAJOR findings present</span>');
    }

    content += '<div class="gate-section">' +
      '<div class="gate-section-label">Comment (optional)</div>' +
      '<textarea id="cd-gate-comment" class="gate-textarea" rows="3" placeholder="Add a comment..."></textarea>' +
      '</div>';

    content += '<div class="gate-actions">' +
      '<button id="cd-final-accept" class="btn btn-success">Accept</button>' +
      '<button id="cd-final-review" class="btn btn-primary">Request Review</button>' +
      '<button id="cd-final-reject" class="btn btn-danger">Reject</button>' +
      '</div>';

    panel.innerHTML = header + content;
    container.appendChild(panel);

    $("#cd-final-accept").addEventListener("click", function () {
      submitCDGate(featureName, "accept");
    });
    $("#cd-final-review").addEventListener("click", function () {
      submitCDGate(featureName, "request_review");
    });
    $("#cd-final-reject").addEventListener("click", function () {
      submitCDGate(featureName, "reject");
    });
  }

  function submitCDGate(featureName, action) {
    var payload = { action: action };

    fetchJSON("/api/codedoc/" + encodeURIComponent(featureName) + "/gate", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload)
    }).then(function (resp) {
      addActivityEntry("CD gate " + action + ": " + featureName, "info");
      clearChildren($("#gate-panels"));
      refreshWorkflowStatusList();
    }).catch(function (err) {
      alert("Gate action failed: " + (err.message || err));
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

  // --- Final Gate: Accept or reject the finished spec ---

  function showFinalGatePanel(featureName, specFileName, specData) {
    var container = $("#gate-panels");
    clearChildren(container);

    var panel = el("div", { className: "gate-panel" });
    var header = '<div style="display:flex;justify-content:space-between;align-items:center;">' +
      '<h3><span class="gate-badge">Final Gate</span> Spec Review — ' + escapeHtml(featureName) + '</h3>' +
      (specData ? '<button id="final-gate-copy" class="btn btn-sm">Copy</button>' : '') +
      '</div>';
    var content = "";

    if (specData) {
      var specText = typeof specData === "string" ? specData : (specData.content || JSON.stringify(specData, null, 2));
      content += '<div class="gate-section">';
      content += '<div class="gate-section-label">' + escapeHtml(specFileName) + '</div>';
      content += '<pre id="final-gate-pre" style="max-height:500px;overflow-y:auto;white-space:pre-wrap;word-break:break-word;font-size:13px;line-height:1.5;padding:12px;background:var(--color-surface-3);color:var(--color-text);border:1px solid var(--color-border);border-radius:4px;">' + escapeHtml(specText) + '</pre>';
      content += '</div>';
    } else {
      content += '<div class="gate-section"><div class="gate-section-value" style="color:var(--color-text-muted);">No spec file found.</div></div>';
    }

    content += '<div class="gate-section" style="margin-top:12px;">';
    content += '<div class="gate-section-label">Comment (optional)</div>';
    content += '<textarea id="final-gate-comment" class="gate-textarea" rows="3" placeholder="Add a comment for rejection or notes..."></textarea>';
    content += '</div>';

    content += '<div class="gate-actions">';
    content += '<button id="final-gate-accept" class="btn btn-success">Accept &amp; Finalize</button>';
    content += '<button id="final-gate-reject" class="btn btn-danger">Reject — Re-review</button>';
    content += '</div>';

    panel.innerHTML = header + content;
    container.appendChild(panel);

    var copyBtn = panel.querySelector("#final-gate-copy");
    if (copyBtn) {
      copyBtn.addEventListener("click", function () {
        var pre = panel.querySelector("#final-gate-pre");
        var text = pre ? pre.textContent : "";
        navigator.clipboard.writeText(text).then(function () {
          copyBtn.textContent = "Copied!";
          setTimeout(function () { copyBtn.textContent = "Copy"; }, 2000);
        }).catch(function () {
          // Fallback: select the text
          var range = document.createRange();
          range.selectNodeContents(pre);
          window.getSelection().removeAllRanges();
          window.getSelection().addRange(range);
        });
      });
    }

    function submitFinalGate(action) {
      var acceptBtn = panel.querySelector("#final-gate-accept");
      var rejectBtn = panel.querySelector("#final-gate-reject");
      var comment = (panel.querySelector("#final-gate-comment") || {}).value || "";
      var payload = { action: action };
      if (comment.trim()) payload.comment = comment.trim();

      // Disable buttons and lock re-render while waiting for transition.
      if (acceptBtn) acceptBtn.disabled = true;
      if (rejectBtn) rejectBtn.disabled = true;
      gatePanelSubmitting = true;

      fetchJSON("/api/tasks/" + encodeURIComponent(featureName) + "/approve", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      }).then(function () {
        clearChildren(container);
        refreshWorkflowStatusList();
        // Clear the lock after enough time for the state to transition.
        setTimeout(function () { gatePanelSubmitting = false; }, 10000);
      }).catch(function (err) {
        gatePanelSubmitting = false;
        if (acceptBtn) acceptBtn.disabled = false;
        if (rejectBtn) rejectBtn.disabled = false;
        alert("Final gate " + action + " failed: " + (err.message || err));
      });
    }

    panel.querySelector("#final-gate-accept").addEventListener("click", function () {
      submitFinalGate("accept");
    });

    panel.querySelector("#final-gate-reject").addEventListener("click", function () {
      submitFinalGate("reject");
    });
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
    var header = '<h3><span class="gate-badge">Human Gate 2</span> Ambiguity Resolution ' +
      '<button id="gate2-copy" class="btn btn-sm" style="background:#eee;color:#333;">Copy</button></h3>';
    var content = "";

    content += '<table class="ambiguity-table">';
    content += "<thead><tr><th>ID</th><th>Section</th><th>Ambiguity</th><th>Agent Assumption</th><th>Question</th><th>Action</th></tr></thead>";
    content += "<tbody>";

    warnings.forEach(function (w, idx) {
      // data-amb-id carries the stable AMB-W-NNN identifier; auto-save
      // uses this (not the ordinal idx) so drafts from a previous drafter
      // round cannot bleed into a new round whose ambiguity IDs have
      // changed. data-idx is still emitted because the submit handler
      // uses it to look up the warning object in the warnings array.
      var ambIdAttr = 'data-amb-id="' + escapeHtml(w.id) + '"';
      content += '<tr class="amb-row" data-idx="' + idx + '" ' + ambIdAttr + '>' +
        "<td>" + escapeHtml(w.id) + "</td>" +
        "<td>" + escapeHtml(w.section) + "</td>" +
        "<td>" + escapeHtml(w.ambiguity) + "</td>" +
        "<td>" + escapeHtml(w.agent_assumption) + "</td>" +
        "<td>" + escapeHtml(w.question_for_user) + "</td>" +
        '<td><select class="amb-action" data-idx="' + idx + '" ' + ambIdAttr + '>' +
        '<option value="accept">Accept assumption</option>' +
        '<option value="answer"' + (gate2AnswerDisabled ? " disabled" : "") + '>Provide answer</option>' +
        '<option value="defer">Defer</option>' +
        "</select></td>" +
        "</tr>";
      // Answer row — spans all columns, hidden by default
      content += '<tr class="amb-answer-row" data-idx="' + idx + '" ' + ambIdAttr + ' style="display:none;">' +
        '<td colspan="6">' +
        '<textarea class="amb-answer" data-idx="' + idx + '" ' + ambIdAttr + ' rows="3" ' +
        'style="width:100%;font-size:13px;padding:8px;border:1px solid var(--color-border);border-radius:4px;resize:vertical;" ' +
        'placeholder="Your answer..."' + (gate2AnswerDisabled ? " disabled" : "") + '></textarea>' +
        '</td></tr>';
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

    // Wire the copy button — formats table as tab-separated text
    var gate2CopyBtn = panel.querySelector("#gate2-copy");
    if (gate2CopyBtn) {
      gate2CopyBtn.addEventListener("click", function () {
        var lines = [];
        lines.push(["ID", "Section", "Ambiguity", "Agent Assumption", "Question"].join("\t"));
        warnings.forEach(function (w) {
          lines.push([w.id, w.section, w.ambiguity, w.agent_assumption, w.question_for_user].join("\t"));
        });
        var text = lines.join("\n");
        navigator.clipboard.writeText(text).then(function () {
          gate2CopyBtn.textContent = "Copied!";
          setTimeout(function () { gate2CopyBtn.textContent = "Copy"; }, 2000);
        }).catch(function () {
          var range = document.createRange();
          range.selectNodeContents(panel.querySelector(".ambiguity-table"));
          window.getSelection().removeAllRanges();
          window.getSelection().addRange(range);
        });
      });
    }

    // Restore saved form state from localStorage.
    var savedGate2 = gateFormLoad(taskId, "gate2");
    restoreGate2FormState(panel, savedGate2);

    // Install auto-save on every input/change.
    installGateAutoSave(panel, taskId, "gate2", collectGate2FormState);

    // Show/hide answer row based on action selection
    $$(".amb-action", panel).forEach(function (sel) {
      sel.addEventListener("change", function () {
        var idx = sel.dataset.idx;
        var answerRow = panel.querySelector(".amb-answer-row[data-idx='" + idx + "']");
        var textarea = panel.querySelector(".amb-answer[data-idx='" + idx + "']");
        if (sel.value === "answer" && !gate2AnswerDisabled) {
          answerRow.style.display = "";
          textarea.disabled = false;
          textarea.focus();
        } else {
          answerRow.style.display = "none";
          textarea.disabled = true;
          textarea.value = "";
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

  // --- Task Human Gate: Task Graph Approval ---

  function showTaskGatePanel(data) {
    var container = $("#gate-panels");
    clearChildren(container);

    var taskId = data.task_id || "";
    var gateData = data.data || {};
    var taskGraphPath = gateData.task_graph_path || "";

    var panel = el("div", { className: "gate-panel" });
    panel.innerHTML =
      '<h3><span class="gate-badge">Task Gate</span> Task Graph Review</h3>' +
      '<p>The task graph has been generated and reviewed. Choose an action:</p>' +
      (taskGraphPath ? '<p class="gate-detail"><strong>Task graph:</strong> <code>' + escapeHtml(taskGraphPath) + '</code></p>' : '') +
      '<div class="gate-form">' +
        '<div class="form-group">' +
          '<label>Comment (optional)</label>' +
          '<textarea id="task-gate-comment" rows="3" placeholder="Feedback for task graph revision..."></textarea>' +
        '</div>' +
        '<div class="gate-actions">' +
          '<button id="task-gate-approve" class="btn btn-success">Approve Tasks</button>' +
          '<button id="task-gate-correct" class="btn btn-warning">Correct (Re-run Taskify)</button>' +
          '<button id="task-gate-skip" class="btn btn-secondary">Skip Task Creation</button>' +
        '</div>' +
      '</div>';

    container.appendChild(panel);

    function submitTaskGate(action) {
      var comment = ($("#task-gate-comment") || {}).value || "";
      var payload = { action: action };
      if (comment.trim()) payload.comment = comment.trim();

      fetchJSON("/api/tasks/" + encodeURIComponent(taskId) + "/approve", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      }).then(function () {
        clearChildren(container);
      }).catch(function (err) {
        alert("Task gate submission failed: " + err.message);
      });
    }

    $("#task-gate-approve").addEventListener("click", function () { submitTaskGate("approve"); });
    $("#task-gate-correct").addEventListener("click", function () { submitTaskGate("correct"); });
    $("#task-gate-skip").addEventListener("click", function () { submitTaskGate("skip"); });
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
    var detail = data.detail || "";
    // Extract the most useful part of the error detail for display.
    // Look for ERROR: or known provider messages.
    var displayDetail = "";
    if (detail) {
      var errorMatch = detail.match(/ERROR:\s*(.+?)(?:\n|$)/);
      if (errorMatch) {
        displayDetail = errorMatch[1].trim();
      } else if (detail.length > 200) {
        displayDetail = detail.substring(0, 200) + "...";
      } else {
        displayDetail = detail;
      }
    }
    var msg = "Agent error: <strong>" + escapeHtml(data.agent) +
      "</strong> — " + escapeHtml(data.error_type) +
      " (retry " + data.retry_count + "/" + data.max_retries + ")";
    if (displayDetail) {
      msg += "<br><small>" + escapeHtml(displayDetail) + "</small>";
    }
    addAlertBanner("error", msg);
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

  function addMessage(source, content, severity, featureName) {
    var container = $("#messages-container");
    if (!container) return;

    var now = new Date();
    var timestamp = String(now.getHours()).padStart(2, "0") + ":" +
                    String(now.getMinutes()).padStart(2, "0") + ":" +
                    String(now.getSeconds()).padStart(2, "0");

    var sevClass = severity ? " msg-" + severity : "";
    var sourceClass = "msg-source-" + source.replace(/[\[\]]/g, "");

    var children = [
      el("span", { className: "msg-timestamp", textContent: timestamp })
    ];

    // Add workflow tag if available
    if (featureName) {
      children.push(el("span", { className: "msg-workflow", textContent: featureName, title: featureName }));
      // Track known workflows for the filter dropdown
      registerMessageWorkflow(featureName);
    }

    children.push(el("span", { className: "msg-source " + sourceClass, textContent: "[" + source + "]" }));
    children.push(el("span", { className: "msg-content", textContent: content }));

    var entry = el("div", { className: "msg-entry" + sevClass, "data-source": source }, children);
    if (featureName) {
      entry.setAttribute("data-workflow", featureName);
    }

    container.appendChild(entry);

    // Apply current filters
    applyMessagesFilter(entry);

    // Auto-scroll
    var autoScroll = $("#msg-auto-scroll");
    if (autoScroll && autoScroll.checked) {
      container.scrollTop = container.scrollHeight;
    }
  }

  // Track known workflows seen in messages for the workflow filter dropdown.
  var knownMessageWorkflows = {};

  function registerMessageWorkflow(featureName) {
    if (!featureName || knownMessageWorkflows[featureName]) return;
    knownMessageWorkflows[featureName] = true;
    var filterSelect = $("#msg-workflow-filter");
    if (!filterSelect) return;
    filterSelect.appendChild(el("option", { value: featureName, textContent: featureName }));
  }

  function applyMessagesFilter(entry) {
    var filterSelect = $("#msg-filter");
    var filterVal = filterSelect ? filterSelect.value : "";
    var wfFilterSelect = $("#msg-workflow-filter");
    var wfFilterVal = wfFilterSelect ? wfFilterSelect.value : "";

    var sourceMatch = true;
    var wfMatch = true;

    if (filterVal) {
      var entrySource = entry.getAttribute("data-source") || "";
      sourceMatch = entrySource === filterVal || entrySource.indexOf(filterVal) !== -1;
    }

    if (wfFilterVal) {
      var entryWorkflow = entry.getAttribute("data-workflow") || "";
      wfMatch = entryWorkflow === wfFilterVal;
    }

    if (sourceMatch && wfMatch) {
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
    var wf = envelope.feature_name || data.feature_name || "";
    switch (envelope.event) {
      case "state_transition":
        addMessage("state", (data.from || "?") + " -> " + (data.to || "?") + " (round " + (data.round || "?") + ")", "", wf);
        break;
      case "agent_dispatch":
        addMessage("agent", "Dispatching " + (data.agent || "?") + " agent", "", wf);
        break;
      case "agent_complete":
        if (data.success) {
          var details = [];
          if (data.duration_ms != null) details.push((data.duration_ms / 1000).toFixed(1) + "s");
          if (data.cost_usd != null) details.push(formatCost(data.cost_usd));
          addMessage("agent", (data.agent || "?") + " completed" + (details.length ? " (" + details.join(", ") + ")" : ""), "success", wf);
        } else {
          addMessage("agent", (data.agent || "?") + " FAILED", "error", wf);
        }
        break;
      case "agent_metrics":
        addMessage("otel", "Tokens: in=" + (data.input_tokens || 0) + " out=" + (data.output_tokens || 0) + " cache=" + (data.cache_read_tokens || 0) + " | Cost: " + formatCost(data.total_cost_usd) + " | API calls: " + (data.total_api_calls || 0), "", wf);
        break;
      case "agent_tool_event":
        var toolStatus = data.success ? "success" : "error";
        var toolAgent = data.agent_name ? "[" + data.agent_name + "] " : "";
        var toolMsg = toolAgent + "Tool: " + (data.tool_name || "?") + " (" + Math.round(data.duration_ms || 0) + "ms) " + (data.success ? "OK" : "FAILED");
        addMessage("otel", toolMsg, toolStatus, wf);
        break;
      case "agent_api_event":
        var apiAgent = data.agent_name ? "[" + data.agent_name + "] " : "";
        var apiDetails = [];
        if (data.duration_ms) apiDetails.push((data.duration_ms / 1000).toFixed(1) + "s");
        if (data.cost_usd) apiDetails.push(formatCost(data.cost_usd));
        if (data.input_tokens || data.output_tokens) apiDetails.push((data.input_tokens || 0) + " in / " + (data.output_tokens || 0) + " out");
        addMessage("otel", apiAgent + "API: " + (data.model || "?") + (apiDetails.length ? " (" + apiDetails.join(", ") + ")" : ""), "", wf);
        break;
      case "workflow_status":
        addMessage("orchestrator", "State: " + (data.state || "?") + " | Round " + (data.round || "?") + " | Cost " + formatCost(data.cost_usd) + " | " + (data.agent_invocations || 0) + " agents", "", wf);
        break;
      case "circuit_breaker":
        addMessage("orchestrator", "Circuit breaker: " + (data.breaker || "?") + " (value=" + data.value + ", limit=" + data.limit + ")", "warning", wf);
        break;
      case "agent_error":
        var errMsg = "Error: " + (data.agent || "?") + " - " + (data.error_type || "?") + " (retry " + (data.retry_count || 0) + "/" + (data.max_retries || 0) + ")";
        if (data.detail) {
          var m = (data.detail || "").match(/ERROR:\s*(.+?)(?:\n|$)/);
          if (m) errMsg += " — " + m[1].trim();
        }
        addMessage("agent", errMsg, "error", wf);
        break;
      case "gate_request":
        addMessage("state", "Human gate: " + (data.gate_type || "?"), "", wf);
        break;
      case "gate_response":
        var severity = data.action === "cancel" ? "error" : (data.action === "correct" ? "warning" : "info");
        addMessage("state", "Gate response: " + (data.action || "?") + " — " + (data.detail || ""), severity, wf);
        break;
    }
  }

  /**
   * Extracts a workflow feature name from a server log line.
   * Matches patterns like:
   *   [otel] metrics [b6-spec]: ...
   *   [workflow] b6-spec completed ...
   *   /specs/b6-spec/discovery-output.json
   *   feature=b6-spec
   *   feature "b6-spec"
   *   feature %q → "b6-spec"
   */
  function extractFeatureFromLog(line) {
    // Pattern 1: [tag] ... [feature-name]: (e.g. otel metrics)
    var m = line.match(/^\[[^\]]+\]\s+\S+\s+\[([^\]]+)\]/);
    if (m) return m[1];

    // Pattern 2: /specs/feature-name/ in file paths
    m = line.match(/\/specs\/([^\/\s]+)\//);
    if (m) return m[1];

    // Pattern 3: feature=name or feature="name" in log key=value pairs
    m = line.match(/feature[=:]\s*"?([a-zA-Z0-9_-]+)"?/);
    if (m) return m[1];

    // Pattern 4: quoted feature name after workflow verbs
    // e.g. [workflow] restarted feature "b6-spec"
    m = line.match(/feature\s+"([^"]+)"/);
    if (m) return m[1];

    return "";
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

        // Extract feature name from server log text
        var featureName = extractFeatureFromLog(line);

        addMessage(source, line, severity, featureName);
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

    // Filter dropdowns
    var filterSelect = $("#msg-filter");
    if (filterSelect) {
      filterSelect.addEventListener("change", applyAllMessagesFilter);
    }
    var wfFilterSelect = $("#msg-workflow-filter");
    if (wfFilterSelect) {
      wfFilterSelect.addEventListener("change", applyAllMessagesFilter);
    }
  }

  // -----------------------------------------------------------------------
  // Workflow Control Buttons (Reset)
  // -----------------------------------------------------------------------

  function runWorkflowReset(workflowType, featureName) {
    if (workflowType === "code_review") {
      return fetchJSON("/api/codereview/" + encodeURIComponent(featureName) + "/reset", {
        method: "POST"
      });
    }
    if (workflowType === "codedoc") {
      return fetchJSON("/api/codedoc/" + encodeURIComponent(featureName) + "/reset", {
        method: "POST"
      });
    }
    return fetchJSON("/api/workflow/reset", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ feature_name: featureName })
    });
  }

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
          updateWorkflowPipeline("IDLE");
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
      var target = selectedFeature || "the running workflow";
      if (!confirm("Cancel workflow \"" + target + "\"?")) return;
      var opts = {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(selectedFeature ? { feature_name: selectedFeature } : {})
      };
      fetchJSON("/api/workflow/cancel", opts)
        .then(function () {
          alert("Workflow cancelled.");
          loadFeatureList();
          refreshWorkflowStatusList();
        })
        .catch(function (err) { alert("Cancel failed: " + err.message); });
    });
  }

  // -----------------------------------------------------------------------
  // Init
  // -----------------------------------------------------------------------

  // -----------------------------------------------------------------------
  // Theme toggle
  // -----------------------------------------------------------------------

  function initTheme() {
    var btn = document.getElementById("btn-theme-toggle");
    var saved = localStorage.getItem("theme");
    if (saved === "light") {
      document.documentElement.setAttribute("data-theme", "light");
      btn.textContent = "Dark Mode";
    }
    btn.addEventListener("click", function () {
      var current = document.documentElement.getAttribute("data-theme");
      if (current === "light") {
        document.documentElement.removeAttribute("data-theme");
        localStorage.setItem("theme", "dark");
        btn.textContent = "Light Mode";
      } else {
        document.documentElement.setAttribute("data-theme", "light");
        localStorage.setItem("theme", "light");
        btn.textContent = "Dark Mode";
      }
    });
  }

  function init() {
    initTheme();
    initTabs();
    initGoalForm();
    initUpload();
    renderDocPicker();
    initSpecControls();
    initIssueFilters();
    initCancelButton();
    initMessages();
    initWorkflowControls();
    initWorkspaceTab();
    wsConnect();
    // Load workspace browser on startup and start auto-refresh
    loadFeatureList();
    startFeatureListPolling();
    // Load the active workflows status list
    refreshWorkflowStatusList();

    // Check if we need to show a gate panel on page load (e.g. after refresh).
    // Also restore persisted OTEL metrics so dashboard data survives refresh.
    fetchJSON("/api/workflow/status").then(function (data) {
      if (!data) return;

      // Handle array format — process each workflow.
      var statuses = Array.isArray(data) ? data : [data];
      renderWorkflowStatusList(statuses);

      // Auto-select on page load: prefer running workflows over terminal ones.
      if (!selectedFeature) {
        var runningWf = null;
        var anyNonIdle = null;
        for (var si = 0; si < statuses.length; si++) {
          var s = statuses[si];
          if (!s || !s.state || !s.feature_name) continue;
          var st = s.state.toUpperCase();
          if (st === "IDLE") continue;
          if (!anyNonIdle) anyNonIdle = s;
          if (s.is_running) { runningWf = s; break; }
        }
        var autoSelect = runningWf || anyNonIdle;
        if (autoSelect) {
          selectedFeature = autoSelect.feature_name;
          renderWorkflowStatusList(statuses);
          updateWorkflowStatus(autoSelect);
        }
      }

      // Process each workflow: restore metrics and show gate panels.
      statuses.forEach(function (status) {
        if (!status || !status.state) return;
        var state = status.state.toUpperCase();
        var feature = status.feature_name;
        if (!feature) return;

        // Restore persisted metrics for active workflows.
        if (state !== "IDLE") {
          if (feature === selectedFeature) {
            restorePersistedMetrics(feature, true);
          }
        }

        if (state === "HUMAN_GATE_1") {
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
        } else if (state === "CR_HUMAN_GATE_SCOPE" || state === "CR_HUMAN_GATE_FIXES") {
          (function (f, s) {
            fetchJSON("/api/codereview/" + encodeURIComponent(f) + "/status").then(function (crStatus) {
              if (!crStatus) return;
              if (s === "CR_HUMAN_GATE_SCOPE") {
                showCRScopeGatePanel(f, crStatus);
              } else {
                showCRFixesGatePanel(f, crStatus);
              }
            }).catch(function () {});
          })(feature, state);
        }
      });
    }).catch(function () {});
  }

  // -----------------------------------------------------------------------
  // Running Agents Tab
  // -----------------------------------------------------------------------

  function loadRunningAgents() {
    fetchJSON("/api/processes").then(function (processes) {
      var tbody = $("#running-agents-body");
      var table = $("#running-agents-table");
      var empty = $("#running-agents-empty");
      if (!tbody || !table || !empty) return;
      clearChildren(tbody);

      if (!processes || processes.length === 0) {
        table.style.display = "none";
        empty.style.display = "";
        return;
      }

      table.style.display = "";
      empty.style.display = "none";

      sortProcesses(processes).forEach(function (p) {
        tbody.appendChild(buildProcessRow(p));
      });
    });
  }

  function sortProcesses(processes) {
    var running = [];
    var terminated = [];
    processes.forEach(function (p) {
      if (p.status === "running") {
        running.push(p);
      } else {
        terminated.push(p);
      }
    });
    running.sort(function (a, b) {
      return (b.started_at || "").localeCompare(a.started_at || "");
    });
    terminated.sort(function (a, b) {
      // Records with ended_at sort before those without (lost records),
      // within each group sort descending by the available timestamp.
      var aHasEnd = a.ended_at ? 1 : 0;
      var bHasEnd = b.ended_at ? 1 : 0;
      if (aHasEnd !== bHasEnd) return bHasEnd - aHasEnd;
      var aKey = a.ended_at || a.started_at || "";
      var bKey = b.ended_at || b.started_at || "";
      return bKey.localeCompare(aKey);
    });
    return running.concat(terminated);
  }

  function buildProcessRow(p) {
    var row = el("tr", { "data-pid": String(p.pid) });

    row.appendChild(el("td", { textContent: p.feature || "-" }));
    row.appendChild(el("td", { textContent: p.role || "-" }));
    var pidCell = el("td", { textContent: String(p.pid), className: "pid-cell" });
    row.appendChild(pidCell);
    row.appendChild(el("td", { textContent: formatTimestamp(p.started_at) }));

    var statusCell = el("td", {});
    var badge = el("span", {
      className: "status-badge " + processStatusClass(p.status),
      textContent: p.status || "unknown"
    });
    statusCell.appendChild(badge);
    row.appendChild(statusCell);

    var actionCell = el("td", {});
    if (p.status === "running") {
      var killBtn = el("button", {
        className: "btn btn-danger btn-sm",
        textContent: "Kill"
      });
      killBtn.addEventListener("click", function () {
        if (!confirm("Kill process PID " + p.pid + "?")) return;
        killBtn.textContent = "Killing\u2026";
        killBtn.disabled = true;
        fetch("/api/processes/" + p.pid + "/kill", { method: "POST" })
          .then(function (resp) {
            if (resp.ok) return;
            return resp.json().then(function (body) {
              var msg = (body && body.error) || ("HTTP " + resp.status);
              alert("Kill failed: " + msg);
              killBtn.textContent = "Kill";
              killBtn.disabled = false;
            });
          })
          .catch(function () {
            killBtn.textContent = "Kill";
            killBtn.disabled = false;
          });
      });
      actionCell.appendChild(killBtn);
    }
    row.appendChild(actionCell);

    return row;
  }

  function processStatusClass(status) {
    switch (status) {
      case "running": return "status-running";
      case "exited": return "status-exited";
      case "killed": return "status-killed";
      case "lost": return "status-lost";
      default: return "";
    }
  }

  function formatTimestamp(ts) {
    if (!ts) return "-";
    try {
      var d = new Date(ts);
      return d.toLocaleString();
    } catch (e) {
      return ts;
    }
  }

  function onProcessStarted(data) {
    var tbody = $("#running-agents-body");
    var table = $("#running-agents-table");
    var empty = $("#running-agents-empty");
    if (!tbody) return;

    // Show table, hide empty state.
    if (table) table.style.display = "";
    if (empty) empty.style.display = "none";

    var row = buildProcessRow({
      feature: data.feature,
      role: data.role,
      pid: data.pid,
      started_at: data.started_at,
      status: "running"
    });
    tbody.insertBefore(row, tbody.firstChild);
  }

  function onProcessEnded(data) {
    updateProcessRow(data.pid, data.status || "exited");
  }

  function onProcessLost(data) {
    updateProcessRow(data.pid, "lost");
  }

  function updateProcessRow(pid, newStatus) {
    var tbody = $("#running-agents-body");
    if (!tbody) return;

    var rows = tbody.querySelectorAll("tr[data-pid='" + pid + "']");
    for (var i = 0; i < rows.length; i++) {
      var row = rows[i];
      // Update status cell (5th cell, index 4).
      var statusCell = row.children[4];
      if (statusCell) {
        clearChildren(statusCell);
        statusCell.appendChild(el("span", {
          className: "status-badge " + processStatusClass(newStatus),
          textContent: newStatus
        }));
      }
      // Remove kill button (6th cell, index 5).
      var actionCell = row.children[5];
      if (actionCell) {
        clearChildren(actionCell);
      }
    }
  }

  // -----------------------------------------------------------------------
  // Workspace Tab
  // -----------------------------------------------------------------------

  function loadWorkspaceFiles() {
    var tbody = $("#workspace-file-body");
    var table = $("#workspace-file-table");
    var empty = $("#workspace-empty");
    if (!tbody || !table || !empty) return;

    if (!selectedFeature) {
      table.style.display = "none";
      empty.style.display = "";
      return;
    }

    fetchJSON("/api/workspace/features/" + encodeURIComponent(selectedFeature) + "/files")
      .then(function (files) {
        clearChildren(tbody);
        hideWorkspaceViewer();

        if (!files || files.length === 0) {
          table.style.display = "none";
          empty.style.display = "";
          return;
        }

        table.style.display = "";
        empty.style.display = "none";

        files.forEach(function (f) {
          var row = el("tr", { className: "workspace-file-row", style: "cursor:pointer" });
          row.appendChild(el("td", { textContent: f.name, className: "workspace-file-name" }));
          row.appendChild(el("td", { textContent: formatFileSize(f.size) }));
          row.appendChild(el("td", { textContent: formatTimestamp(f.modified) }));
          row.addEventListener("click", function () { openWorkspaceFile(f.name); });
          tbody.appendChild(row);
        });
      })
      .catch(function () {
        table.style.display = "none";
        empty.style.display = "";
      });
  }

  function openWorkspaceFile(filename) {
    var viewer = $("#workspace-file-viewer");
    var titleEl = $("#workspace-viewer-filename");
    var contentEl = $("#workspace-viewer-content");
    if (!viewer || !titleEl || !contentEl) return;

    titleEl.textContent = filename;
    contentEl.textContent = "Loading…";
    viewer.style.display = "";

    fetchJSON("/api/workspace/features/" + encodeURIComponent(selectedFeature) + "/files/" + encodeURIComponent(filename))
      .then(function (data) {
        var text;
        if (typeof data === "string") {
          text = data;
        } else if (data && typeof data.content === "string") {
          text = data.content;
        } else {
          text = JSON.stringify(data, null, 2);
        }
        contentEl.textContent = text;
      })
      .catch(function (err) {
        contentEl.textContent = "Error loading file: " + err.message;
      });
  }

  function hideWorkspaceViewer() {
    var viewer = $("#workspace-file-viewer");
    if (viewer) viewer.style.display = "none";
  }

  function formatFileSize(bytes) {
    if (bytes == null) return "-";
    if (bytes < 1024) return bytes + " B";
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KB";
    return (bytes / (1024 * 1024)).toFixed(1) + " MB";
  }

  function initWorkspaceTab() {
    var closeBtn = $("#workspace-viewer-close");
    if (closeBtn) closeBtn.addEventListener("click", hideWorkspaceViewer);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
