// Cloudflare Email Routing handler: forwards every inbound message to lifemail.
//
// Deploy with:
//   npx wrangler deploy
//   npx wrangler secret put LIFEMAIL_INBOUND_TOKEN
//
// Then add a catch-all (or per-address) routing rule for the Worker in the
// Cloudflare dashboard: Email Service -> Email Routing -> Routing rules.

export default {
  async email(message, env, ctx) {
    const raw = await new Response(message.raw).arrayBuffer();

    const headers = new Headers({
      "Authorization": `Bearer ${env.LIFEMAIL_INBOUND_TOKEN}`,
      "Content-Type": "message/rfc822",
      // message.to is the envelope recipient (RCAPT TO), which is reliable
      // even for Bcc or catch-all addresses.
      "X-Envelope-To": message.to,
      "X-Envelope-From": message.from,
    });

    let response;
    try {
      response = await fetch(env.LIFEMAIL_INBOUND_URL, {
        method: "POST",
        headers,
        body: raw,
      });
    } catch (err) {
      // Throwing makes Cloudflare report a temporary failure so the sending
      // MTA retries instead of bouncing the message.
      throw new Error(`lifemail unreachable: ${err}`);
    }

    if (!response.ok) {
      const detail = await response.text();
      // 550 means lifemail cannot route the recipient at all (unknown address
      // and no catch-all configured). Rejecting permanently makes the sending
      // MTA bounce immediately instead of retrying for days.
      if (response.status === 550) {
        message.setReject(detail || "unknown recipient");
        return;
      }
      // Everything else (5xx storage/forward failures) is transient: throwing
      // makes Cloudflare report a temporary failure so the sender retries.
      throw new Error(`lifemail rejected message: ${response.status} ${detail}`);
    }
  },
};
