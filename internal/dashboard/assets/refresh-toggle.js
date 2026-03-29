// Pause/resume HTMX polling on the dashboard.
// Finds elements with hx-trigger containing 'every' and toggles their polling.
// SSE elements (no hx-trigger) are unaffected.
document.addEventListener("DOMContentLoaded", function () {
    var btn = document.getElementById("refresh-toggle");
    if (!btn) return;

    var paused = false;

    btn.addEventListener("click", function () {
        paused = !paused;

        var pollingEls = document.querySelectorAll("[hx-trigger]");
        pollingEls.forEach(function (el) {
            var trigger = el.getAttribute("hx-trigger");
            if (!trigger || trigger.indexOf("every") === -1) return;

            if (paused) {
                el.dataset.pausedTrigger = trigger;
                el.removeAttribute("hx-trigger");
            } else {
                if (el.dataset.pausedTrigger) {
                    el.setAttribute("hx-trigger", el.dataset.pausedTrigger);
                    delete el.dataset.pausedTrigger;
                    htmx.process(el);
                }
            }
        });

        btn.textContent = paused ? "Resume" : "Pause";
    });
});
