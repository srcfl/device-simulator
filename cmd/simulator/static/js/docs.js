(function () {
    const tabs = document.querySelectorAll('.docs-tab');
    const panels = document.querySelectorAll('.docs-panel');

    function selectTab(name) {
        tabs.forEach((tab) => {
            tab.classList.toggle('active', tab.dataset.tab === name);
        });
        panels.forEach((panel) => {
            panel.classList.toggle('active', panel.id === `tab-${name}`);
        });
    }

    tabs.forEach((tab) => {
        tab.addEventListener('click', () => selectTab(tab.dataset.tab));
    });

    function setBaseUrls() {
        const origin = window.location.origin;
        document.querySelectorAll('[data-base-url]').forEach((el) => {
            el.textContent = origin;
        });
    }

    async function loadOpenAPI() {
        // Swagger UI is now loaded in an iframe to avoid CSS conflicts.
        return;
    }

    setBaseUrls();
    loadOpenAPI();
})();
