// canal.js — envio da denúncia em pedaços, com barra de progresso.
//
// O proxy da VPS corta requisição com mais de 60 s, e um arquivo de centenas de MB
// num 4G passa disso. Então: abre um rascunho, manda cada arquivo em partes de
// 8 MB (cada parte com até 4 tentativas) e conclui com o texto. Do arquivo, só sai
// o conteúdo e a EXTENSÃO; o nome nunca é enviado (pode ter o nome da pessoa).
// Cada arquivo é identificado pela posição no rascunho; o id interno nunca chega aqui.
//
// Sem JavaScript, o formulário vai inteiro para POST /denuncias (anexos pequenos).
// Este arquivo não guarda nada no navegador e não lê nada sobre o aparelho.
"use strict"
;(() => {
  const form = document.getElementById("form-denuncia")
  if (!form) return
  const canal = document.getElementById("canal")
  const flash = document.getElementById("flash")
  const progresso = document.getElementById("progresso")
  const barra = progresso.querySelector("progress")
  const rotulo = progresso.querySelector("span")
  const botao = form.querySelector("button")
  const maxTotal = Number(form.dataset.maxTotal)
  const maxArquivos = Number(form.dataset.maxArquivos)
  const maxRotulo = form.dataset.maxRotulo

  class Recusa extends Error {}

  function extensao(nome) {
    const m = /\.([a-z0-9]{1,10})$/i.exec(nome)
    return m ? m[1].toLowerCase() : ""
  }

  async function pedir(url, opcoes) {
    const r = await fetch(url, { credentials: "omit", cache: "no-store", ...opcoes })
    if (r.ok) return r
    const msg = (await r.text()).trim() || `erro ${r.status}`
    // 4xx é recusa do servidor (não adianta repetir); 5xx e rede caída, sim.
    throw r.status < 500 ? new Recusa(msg) : new Error(msg)
  }

  async function comTentativas(fn) {
    for (let t = 1; ; t++) {
      try {
        return await fn()
      } catch (e) {
        if (e instanceof Recusa || t === 4) throw e
        await new Promise((ok) => setTimeout(ok, 1500 * t))
      }
    }
  }

  function campos(obj) {
    const f = new FormData()
    for (const [k, v] of Object.entries(obj)) f.append(k, v)
    return f
  }

  function mostrar(enviados, total) {
    const pct = total ? Math.floor((enviados / total) * 100) : 100
    barra.value = pct
    rotulo.textContent = total ? `Enviando arquivos: ${pct}%` : "Enviando…"
  }

  async function enviarArquivo(envio, arquivo, tamParte, avancar) {
    const r = await comTentativas(() =>
      pedir(`/envios/${envio}/arquivos`, {
        method: "POST",
        body: campos({ tamanho: arquivo.size, extensao: extensao(arquivo.name) }),
      }),
    )
    const { posicao } = await r.json()
    for (let n = 0, ini = 0; ini < arquivo.size; n++, ini += tamParte) {
      const parte = arquivo.slice(ini, Math.min(arquivo.size, ini + tamParte))
      await comTentativas(() => pedir(`/envios/${envio}/arquivos/${posicao}/partes/${n}`, { method: "PUT", body: parte }))
      avancar(parte.size)
    }
  }

  async function enviar() {
    const arquivos = [...form.arquivos.files].filter((a) => a.size > 0)
    const total = arquivos.reduce((s, a) => s + a.size, 0)
    if (arquivos.length > maxArquivos) throw new Recusa(`Envie no máximo ${maxArquivos} arquivos.`)
    if (total > maxTotal) throw new Recusa(`Os arquivos passam de ${maxRotulo} no total.`)

    progresso.hidden = false
    let enviados = 0
    mostrar(0, total)
    const r = await comTentativas(() => pedir("/envios", { method: "POST" }))
    const { id: envio, tamParte } = await r.json()
    for (const arquivo of arquivos) {
      await enviarArquivo(envio, arquivo, tamParte, (n) => mostrar((enviados += n), total))
    }
    rotulo.textContent = "Concluindo…"
    const fim = await pedir(`/envios/${envio}/concluir`, {
      method: "POST",
      body: campos({ categoria: form.categoria.value, descricao: form.descricao.value }),
    })
    canal.innerHTML = await fim.text()
  }

  form.addEventListener("submit", async (ev) => {
    ev.preventDefault()
    flash.textContent = ""
    botao.disabled = true
    try {
      await enviar()
    } catch (e) {
      flash.textContent = e instanceof Recusa ? e.message : "Não foi possível enviar. Confira a conexão e tente de novo."
      progresso.hidden = true
      botao.disabled = false
    }
  })
})()
