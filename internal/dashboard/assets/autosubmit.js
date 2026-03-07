// Auto-submit forms when select/checkbox values change.
// Replaces inline onchange="this.form.submit()" handlers to comply with CSP.
document.addEventListener("DOMContentLoaded", function () {
    document.querySelectorAll("[data-autosubmit]").forEach(function (el) {
        el.addEventListener("change", function () {
            this.form.submit();
        });
    });
});
