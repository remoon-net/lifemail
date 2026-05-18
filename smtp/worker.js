export default {
  async fetch(request, env, ctx) {
    // You can view your logs in the Observability dashboard
    console.info({ message: "Hello World Worker received a request!" })
    return new Response("Hello World v0.0.5!")
  },
  /**
   *
   * @param {{from:string;to:string;raw:any;setReject:(reason:string)=>void}} message
   * @param {{HTTP_SMTP_API:string;HTTP_SMTP_KEY:string}} env
   * @param {any} ctx
   * @returns
   */
  async email(message, env, ctx) {
    let api = env.HTTP_SMTP_API
    let key = env.HTTP_SMTP_KEY
    if (!key || !api) {
      message.setReject("Lost Env HTTP_SMTP_KEY or HTTP_SMTP_API")
      return
    }

    let from = message.from
    let to = message.to
    let dStr = new Date().toISOString()
    let msg = from + to + dStr
    let sign = await this.createSign(key, msg)
    let req = new Request(api, {
      method: "POST",
      body: message.raw,
      headers: {
        "X-SMTP-From": from,
        "X-SMTP-To": to,
        "X-SMTP-Datetime": dStr,
        "X-SMTP-Sign": sign,
      },
    })
    let f = fetch(req)
    let ok = await f.then(() => true).catch(() => false)
    if (!ok) {
      message.setReject("fetch HTTP_SMTP failed")
      return
    }
    let resp = await f
    if (resp.status != 204) {
      let msg = await resp.text()
      message.setReject("send failed: " + msg)
    }
  },
  async createSign(secret, message) {
    const enc = new TextEncoder()
    const key = await crypto.subtle.importKey(
      "raw",
      enc.encode(secret),
      { name: "HMAC", hash: "SHA-256" },
      false,
      ["sign"]
    )
    const buf = await crypto.subtle.sign("HMAC", key, enc.encode(message))
    const signature = btoa(String.fromCharCode(...new Uint8Array(buf)))
    return signature
  },
}
