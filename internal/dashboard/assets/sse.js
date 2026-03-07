// SSE escalation listener for the dashboard.
(function() {
    if (typeof EventSource === 'undefined') return;
    var es = new EventSource('/api/events');
    es.addEventListener('escalation', function(e) {
        var data = JSON.parse(e.data);
        var banner = document.getElementById('alert-banner');
        if (banner) {
            banner.className = 'active';
            banner.innerHTML = data.count + ' escalation(s) need attention — <a href="/messages">View messages</a>';
        }
    });
    es.addEventListener('connected', function(e) {
        console.log('SSE connected');
    });
    es.onerror = function() {
        console.log('SSE connection lost, falling back to HTMX polling');
    };
})();
