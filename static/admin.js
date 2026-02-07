// ---- Utilities ----

function debounce(fn, ms) {
    var timer;
    return function() {
        var args = arguments;
        var ctx = this;
        clearTimeout(timer);
        timer = setTimeout(function() { fn.apply(ctx, args); }, ms);
    };
}

// ---- Save indicator ----

var saveIndicator;
(function() {
    saveIndicator = document.createElement('div');
    saveIndicator.className = 'save-indicator';
    document.body.appendChild(saveIndicator);
})();

function showSave(state, msg) {
    saveIndicator.textContent = msg;
    saveIndicator.className = 'save-indicator visible ' + state;
    if (state === 'saved') {
        setTimeout(function() { saveIndicator.className = 'save-indicator'; }, 1500);
    }
}

function apiPost(url, data) {
    return fetch(url, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(data)
    }).then(function(resp) {
        if (!resp.ok) return resp.text().then(function(t) { throw new Error(t); });
        return resp.json();
    });
}

// ---- Copy public link ----

function copyLink() {
    var el = document.getElementById('public-url');
    if (el) {
        navigator.clipboard.writeText(el.textContent.trim());
        var btn = el.nextElementSibling;
        if (btn) {
            var orig = btn.innerHTML;
            btn.innerHTML = '<i class="fa-solid fa-check"></i> ' + (btn.getAttribute('data-copied') || 'Copied!');
            setTimeout(function() { btn.innerHTML = orig; }, 1500);
        }
    }
}

// ---- Auto-save event details ----

var saveEvent = debounce(function(eventId) {
    var data = {
        event_id: eventId,
        title_fr: document.getElementById('title_fr').value,
        title_en: document.getElementById('title_en').value,
        description_fr: document.getElementById('description_fr').value,
        description_en: document.getElementById('description_en').value,
        event_date: document.getElementById('event_date').value,
        event_time: document.getElementById('event_time').value
    };
    showSave('', 'Saving...');
    apiPost('/admin/api/event/save', data)
        .then(function() { showSave('saved', 'Saved'); })
        .catch(function() { showSave('error', 'Save failed'); });
}, 500);

function initEventAutoSave() {
    var form = document.querySelector('#event-details [data-event-id]');
    if (!form) return;
    var eventId = parseInt(form.dataset.eventId);
    form.addEventListener('input', function() { saveEvent(eventId); });
    form.addEventListener('change', function() { saveEvent(eventId); });
}

// ---- Auto-save groups ----

// Per-group debounced savers
var groupSavers = {};
function getGroupSaver(groupId) {
    if (!groupSavers[groupId]) {
        groupSavers[groupId] = debounce(function(data) {
            showSave('', 'Saving...');
            apiPost('/admin/api/group/save', data)
                .then(function() { showSave('saved', 'Saved'); })
                .catch(function() { showSave('error', 'Save failed'); });
        }, 500);
    }
    return groupSavers[groupId];
}

function saveGroup(el) {
    var item = el.closest('[data-type="group"]');
    if (!item) return;
    var id = parseInt(item.dataset.id);
    var data = {
        id: id,
        title_fr: item.querySelector('[data-field="title_fr"]').value,
        title_en: item.querySelector('[data-field="title_en"]').value
    };
    getGroupSaver(id)(data);
}

// ---- Auto-save tasks ----

var taskSavers = {};
function getTaskSaver(taskId) {
    if (!taskSavers[taskId]) {
        taskSavers[taskId] = debounce(function(data) {
            showSave('', 'Saving...');
            apiPost('/admin/api/task/save', data)
                .then(function() { showSave('saved', 'Saved'); })
                .catch(function() { showSave('error', 'Save failed'); });
        }, 500);
    }
    return taskSavers[taskId];
}

function saveTask(el) {
    var item = el.closest('[data-type="task"]');
    if (!item) return;
    var id = parseInt(item.dataset.id);
    var msInput = item.querySelector('[data-field="max_slots"]');
    var msVal = msInput ? msInput.value.trim() : '';
    var data = {
        id: id,
        title_fr: (item.querySelector('[data-field="title_fr"]') || {}).value || '',
        title_en: (item.querySelector('[data-field="title_en"]') || {}).value || '',
        description_fr: (item.querySelector('[data-field="description_fr"]') || {}).value || '',
        description_en: (item.querySelector('[data-field="description_en"]') || {}).value || '',
        max_slots: msVal === '' ? null : parseInt(msVal)
    };
    getTaskSaver(id)(data);
}

// ---- Delegated input handler for tree items ----

function initTreeAutoSave() {
    var root = document.getElementById('sortable-container');
    if (!root) return;

    root.addEventListener('input', function(e) {
        if (!e.target.dataset || !e.target.dataset.field) return;
        var type = e.target.closest('[data-type]');
        if (!type) return;
        if (type.dataset.type === 'group') saveGroup(e.target);
        else if (type.dataset.type === 'task') saveTask(e.target);
    });

    root.addEventListener('change', function(e) {
        if (!e.target.dataset || !e.target.dataset.field) return;
        var type = e.target.closest('[data-type]');
        if (!type) return;
        if (type.dataset.type === 'group') saveGroup(e.target);
        else if (type.dataset.type === 'task') saveTask(e.target);
    });
}

// ---- Create new group / task ----

function createGroup() {
    var container = document.getElementById('sortable-container');
    if (!container) return;
    var eventId = parseInt(container.dataset.eventId);
    apiPost('/admin/api/group/create', { event_id: eventId })
        .then(function() { location.reload(); })
        .catch(function() { showSave('error', 'Create failed'); });
}

function createTask() {
    var container = document.getElementById('sortable-container');
    if (!container) return;
    var eventId = parseInt(container.dataset.eventId);
    apiPost('/admin/api/task/create', { event_id: eventId })
        .then(function() { location.reload(); })
        .catch(function() { showSave('error', 'Create failed'); });
}

// ---- Delete item ----

function deleteItem(type, id) {
    if (!confirm(type === 'group' ? 'Delete this group?' : 'Delete this task?')) return;
    var url = type === 'group' ? '/admin/api/group/delete' : '/admin/api/task/delete';
    apiPost(url, { id: id }).then(function() {
        // Remove element from DOM
        var el = document.querySelector('[data-type="' + type + '"][data-id="' + id + '"]');
        if (el) el.remove();
        updatePlaceholders();
    }).catch(function() { showSave('error', 'Delete failed'); });
}

// ---- Toggle description ----

function toggleDescription(btn) {
    var taskEl = btn.closest('[data-type="task"]');
    if (!taskEl) return;
    var descSection = taskEl.querySelector('.task-descriptions');
    if (!descSection) return;
    if (descSection.classList.contains('desc-hidden')) {
        descSection.classList.remove('desc-hidden');
        btn.textContent = btn.dataset.hideText || 'hide description';
        var firstInput = descSection.querySelector('textarea');
        if (firstInput) firstInput.focus();
    } else {
        descSection.classList.add('desc-hidden');
        btn.textContent = btn.dataset.showText || 'add description';
    }
}

// ---- AI text import ----

function aiParse(mode) {
    var text = document.getElementById('ai-text');
    var status = document.getElementById('ai-status');
    if (!text || !status) return;
    if (!text.value.trim()) { text.focus(); return; }

    var container = document.getElementById('sortable-container');
    var eventId = container ? parseInt(container.dataset.eventId) : 0;
    if (!eventId) return;

    var defaultOne = document.getElementById('ai-default-one');
    var defaultOneChecked = defaultOne && defaultOne.checked;

    status.style.display = 'block';
    status.className = 'ai-status ai-status-loading';
    status.textContent = status.dataset.loading || 'Processing...';

    var btns = document.querySelectorAll('#ai-import .btn');
    btns.forEach(function(b) { b.disabled = true; });

    apiPost('/admin/api/ai-parse', {
        event_id: eventId, mode: mode, text: text.value, default_one: defaultOneChecked
    }).then(function() {
        status.className = 'ai-status ai-status-success';
        status.textContent = status.dataset.success || 'Done!';
        setTimeout(function() { location.reload(); }, 800);
    }).catch(function(err) {
        status.className = 'ai-status ai-status-error';
        status.textContent = (status.dataset.error || 'Error') + ': ' + err.message;
        btns.forEach(function(b) { b.disabled = false; });
    });
}

// ---- SortableJS drag-and-drop ----

(function() {
    var container = document.getElementById('sortable-container');
    if (!container) return;
    if (typeof Sortable === 'undefined') return;

    var sortableOpts = {
        group: 'tree-items',
        handle: '.drag-handle',
        animation: 150,
        fallbackOnBody: true,
        swapThreshold: 0.65,
        draggable: '[data-type]',
        ghostClass: 'sortable-ghost',
        chosenClass: 'sortable-chosen',
        onEnd: function() {
            updatePlaceholders();
            saveOrder();
        }
    };

    new Sortable(container, sortableOpts);

    container.querySelectorAll('.tree-children').forEach(function(el) {
        new Sortable(el, sortableOpts);
    });

    function saveOrder() {
        var tree = serializeTree(container);
        apiPost('/admin/api/reorder', tree);
    }

    function serializeTree(el) {
        var nodes = [];
        for (var i = 0; i < el.children.length; i++) {
            var child = el.children[i];
            if (!child.dataset || !child.dataset.type) continue;
            var node = { type: child.dataset.type, id: parseInt(child.dataset.id) };
            if (node.type === 'group') {
                var childrenEl = null;
                for (var k = 0; k < child.children.length; k++) {
                    if (child.children[k].classList.contains('tree-children')) {
                        childrenEl = child.children[k];
                        break;
                    }
                }
                if (childrenEl) {
                    node.children = serializeTree(childrenEl);
                    if (!childrenEl._sortable) {
                        childrenEl._sortable = new Sortable(childrenEl, sortableOpts);
                    }
                }
            }
            nodes.push(node);
        }
        return nodes;
    }

    container.querySelectorAll('.tree-children').forEach(function(el) {
        el._sortable = true;
    });
})();

// ---- Placeholder visibility ----

function updatePlaceholders() {
    document.querySelectorAll('.tree-children, .tree-root').forEach(function(el) {
        var hasItems = false;
        for (var i = 0; i < el.children.length; i++) {
            if (el.children[i].dataset && el.children[i].dataset.type) {
                hasItems = true;
                break;
            }
        }
        var placeholder = null;
        for (var i = 0; i < el.children.length; i++) {
            if (el.children[i].classList.contains('drop-placeholder')) {
                placeholder = el.children[i];
                break;
            }
        }
        if (placeholder) {
            placeholder.style.display = hasItems ? 'none' : 'block';
        }
    });
}

// ---- Init ----

initEventAutoSave();
initTreeAutoSave();
updatePlaceholders();
