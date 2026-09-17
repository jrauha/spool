document.addEventListener("click", (event) => {
  const link = event.target.closest("a[data-read-url]");
  if (!link || event.defaultPrevented || event.button !== 0) {
    return;
  }

  const body = new URLSearchParams({
    csrf_token: link.dataset.csrfToken,
    return_to: `${location.pathname}${location.search}`,
  });

  fetch(link.dataset.readUrl, {
    method: "POST",
    body,
    credentials: "same-origin",
    keepalive: true,
    redirect: "manual",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
  }).catch(() => {});
});
