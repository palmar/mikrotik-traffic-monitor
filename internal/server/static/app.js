(function () {
  "use strict";

  var MAX_POINTS = 240;
  var interfaces = [];
  var ifaceData = {};
  var activeIface = null;
  var plot = null;

  var statusEl = document.getElementById("status");
  var currentInEl = document.getElementById("current-in");
  var currentOutEl = document.getElementById("current-out");
  var peakInEl = document.getElementById("peak-in");
  var peakOutEl = document.getElementById("peak-out");
  var avgInEl = document.getElementById("avg-in");
  var avgOutEl = document.getElementById("avg-out");
  var metaEl = document.getElementById("meta");
  var tabsEl = document.getElementById("tabs");

  function formatRate(bps) {
    if (bps == null || isNaN(bps)) return "\u2014";
    if (bps >= 1e9) return (bps / 1e9).toFixed(2) + " Gbps";
    if (bps >= 1e6) return (bps / 1e6).toFixed(2) + " Mbps";
    if (bps >= 1e3) return (bps / 1e3).toFixed(1) + " Kbps";
    return bps.toFixed(0) + " bps";
  }

  function avg(arr) {
    if (arr.length === 0) return 0;
    return arr.reduce(function (a, b) { return a + b; }, 0) / arr.length;
  }

  function ensureIfaceData(name) {
    if (!ifaceData[name]) {
      ifaceData[name] = {
        timestamps: [],
        inData: [],
        outData: [],
        peakIn: 0,
        peakOut: 0
      };
    }
    return ifaceData[name];
  }

  function selectInterface(name) {
    activeIface = name;
    if (tabsEl) {
      var buttons = tabsEl.querySelectorAll("button");
      for (var i = 0; i < buttons.length; i++) {
        buttons[i].className = buttons[i].dataset.iface === name ? "tab active" : "tab";
      }
    }
    updateChart();
    updateStats();
  }

  function renderTabs() {
    if (!tabsEl || interfaces.length <= 1) return;
    tabsEl.innerHTML = "";
    interfaces.forEach(function (name) {
      var btn = document.createElement("button");
      btn.textContent = name;
      btn.className = name === activeIface ? "tab active" : "tab";
      btn.dataset.iface = name;
      btn.addEventListener("click", function () { selectInterface(name); });
      tabsEl.appendChild(btn);
    });
  }

  function updateStats() {
    if (!activeIface) return;
    var d = ifaceData[activeIface];
    if (!d) return;
    var lastIn = d.inData.length > 0 ? d.inData[d.inData.length - 1] : null;
    var lastOut = d.outData.length > 0 ? d.outData[d.outData.length - 1] : null;
    currentInEl.textContent = formatRate(lastIn);
    currentOutEl.textContent = formatRate(lastOut);
    peakInEl.textContent = formatRate(d.peakIn);
    peakOutEl.textContent = formatRate(d.peakOut);
    avgInEl.textContent = formatRate(avg(d.inData));
    avgOutEl.textContent = formatRate(avg(d.outData));
    metaEl.textContent = activeIface + " \u00b7 " + d.inData.length + " samples";
  }

  function updateChart() {
    if (!activeIface || !plot) return;
    var d = ifaceData[activeIface];
    if (!d) return;
    plot.setData([d.timestamps, d.inData, d.outData]);
  }

  function initChart() {
    var chartEl = document.getElementById("chart");
    var width = chartEl.clientWidth - 32;
    var opts = {
      width: width,
      height: 280,
      cursor: { show: true },
      scales: {
        x: { time: true },
        y: {
          auto: true,
          range: function (u, dMin, dMax) {
            return [0, Math.max(dMax * 1.1, 1000)];
          }
        }
      },
      axes: [
        {
          stroke: "#8b8fa3",
          grid: { stroke: "rgba(42,45,58,0.6)", width: 1 },
          ticks: { stroke: "rgba(42,45,58,0.6)", width: 1 },
          font: "11px system-ui",
        },
        {
          stroke: "#8b8fa3",
          grid: { stroke: "rgba(42,45,58,0.6)", width: 1 },
          ticks: { stroke: "rgba(42,45,58,0.6)", width: 1 },
          font: "11px system-ui",
          values: function (u, vals) {
            return vals.map(formatRate);
          },
          size: 80,
        }
      ],
      series: [
        {},
        {
          label: "Inbound",
          stroke: "#4ade80",
          width: 2,
          fill: "rgba(74,222,128,0.08)",
        },
        {
          label: "Outbound",
          stroke: "#60a5fa",
          width: 2,
          fill: "rgba(96,165,250,0.08)",
        }
      ],
    };

    var d = activeIface ? ifaceData[activeIface] : null;
    var ts = d ? d.timestamps : [];
    var inD = d ? d.inData : [];
    var outD = d ? d.outData : [];
    plot = new uPlot(opts, [ts, inD, outD], chartEl);

    window.addEventListener("resize", function () {
      var w = chartEl.clientWidth - 32;
      if (w > 0) plot.setSize({ width: w, height: 280 });
    });
  }

  function addSample(iface, sample) {
    var d = ensureIfaceData(iface);
    d.timestamps.push(sample.ts);
    d.inData.push(sample.in_bps);
    d.outData.push(sample.out_bps);

    if (sample.in_bps > d.peakIn) d.peakIn = sample.in_bps;
    if (sample.out_bps > d.peakOut) d.peakOut = sample.out_bps;

    while (d.timestamps.length > MAX_POINTS) {
      d.timestamps.shift();
      d.inData.shift();
      d.outData.shift();
    }

    if (iface === activeIface) {
      if (plot) plot.setData([d.timestamps, d.inData, d.outData]);
      updateStats();
    }
  }

  function connectSSE() {
    var es = new EventSource("/stream");

    es.onopen = function () {
      statusEl.textContent = "Connected";
      statusEl.className = "status connected";
    };

    es.onmessage = function (e) {
      try {
        var msg = JSON.parse(e.data);
        addSample(msg.iface, { ts: msg.ts, in_bps: msg.in_bps, out_bps: msg.out_bps });
      } catch (err) {
        console.error("parse error:", err);
      }
    };

    es.onerror = function () {
      statusEl.textContent = "Disconnected \u2014 reconnecting\u2026";
      statusEl.className = "status error";
    };
  }

  function init() {
    fetch("/interfaces")
      .then(function (r) { return r.json(); })
      .then(function (ifaces) {
        interfaces = ifaces || [];
        interfaces.forEach(ensureIfaceData);
        activeIface = interfaces[0] || null;
        renderTabs();
        return fetch("/history");
      })
      .then(function (r) { return r.json(); })
      .then(function (history) {
        Object.keys(history).forEach(function (name) {
          var d = ensureIfaceData(name);
          var samples = history[name] || [];
          samples.forEach(function (s) {
            d.timestamps.push(s.ts);
            d.inData.push(s.in_bps);
            d.outData.push(s.out_bps);
            if (s.in_bps > d.peakIn) d.peakIn = s.in_bps;
            if (s.out_bps > d.peakOut) d.peakOut = s.out_bps;
          });
        });
        initChart();
        updateStats();
        connectSSE();
      })
      .catch(function (err) {
        console.error("init failed:", err);
        initChart();
        connectSSE();
      });
  }

  init();
})();
