(function(){const r=document.createElement("link").relList;if(r&&r.supports&&r.supports("modulepreload"))return;for(const t of document.querySelectorAll('link[rel="modulepreload"]'))c(t);new MutationObserver(t=>{for(const n of t)if(n.type==="childList")for(const i of n.addedNodes)i.tagName==="LINK"&&i.rel==="modulepreload"&&c(i)}).observe(document,{childList:!0,subtree:!0});function l(t){const n={};return t.integrity&&(n.integrity=t.integrity),t.referrerPolicy&&(n.referrerPolicy=t.referrerPolicy),t.crossOrigin==="use-credentials"?n.credentials="include":t.crossOrigin==="anonymous"?n.credentials="omit":n.credentials="same-origin",n}function c(t){if(t.ep)return;t.ep=!0;const n=l(t);fetch(t.href,n)}})();async function o(){const e=await fetch("/api/me");if(e.status===401)return null;if(!e.ok)throw new Error(`GET /api/me: ${e.status}`);return e.json()}async function s(){const e=await fetch("/api/steam/recheck",{method:"POST"});if(!e.ok)throw new Error(`POST /api/steam/recheck: ${e.status}`)}const d=document.getElementById("app");async function u(){const e=await o();if(!e){d.innerHTML=`
      <h1>Game Status Sync</h1>
      <input id="handle" placeholder="your.handle" />
      <button id="signin">Sign in with atproto</button>
    `,document.getElementById("signin").addEventListener("click",()=>{const r=document.getElementById("handle").value;window.location.href=`/login?handle=${encodeURIComponent(r)}`});return}if(!e.steamSubject){await s().catch(()=>{}),a(await o()??e);return}a(e)}function a(e){const r=e.steamSubject?`Verified as ${e.steamDisplayName??e.steamSubject}`:"Not connected — verify at keytrace.dev, then recheck below";d.innerHTML=`
    <h1>Game Status Sync</h1>
    <p>Signed in as ${e.did}</p>
    <h2>Steam</h2>
    <p>${r}</p>
    <button id="recheck">Recheck claim</button>
  `,document.getElementById("recheck").addEventListener("click",async()=>{await s(),await u()})}u();
