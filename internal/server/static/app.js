(function () {
  "use strict";

  const MAX_POINTS = 240; // ~20 min at 5s intervals
  let timestamps = [];
  let inData = [];
  let outData = [];
  let peakIn = 0;
  let peakOut = 0;
  let plot = null;

  const statusEl = document.getElementById("status");
  const currentInEl = document.getElementById("current-in");
  const currentOutEl = document.getElementById("current-out");
  const peakInEl = document.getElementById("peak-in");
  const peakOutEl = document.getElementById("peak-out");
  const avgInEl = document.getElementById("avg-in");
  const avgOutEl = document.getElementById("avg-out");
  const metaEl = document.getElementById("meta");

  function formatRate(bps) {
    if (bps == null || isNaN(bps)) return "—";
    if (bps >= 1e9) return (bps / 1e9).toFixed(2) + " Gbps";
    if (bps >= 1e6) return (bps / 1e6).toFixed(2) + " Mbps";
    if (bps >= 1e3) return (bps / 1e3).toFixed(1) + " Kbps";
    return bps.toFixed(0) + " bps";
  }

  function avg(arr) {
    if (arr.length === 0) return 0;
    return arr.reduce(function (a, b) { return a + b; }, 0) / arr.length;
  }

  function updateStats() {
    var lastIn = inData.length > 0 ? inData[inData.length - 1] : null;
    var lastOut = outData.length > 0 ? outData[outData.length - 1] : null;
    currentInEl.textContent = formatRate(lastIn);
    currentOutEl.textContent = formatRate(lastOut);
    peakInEl.textContent = formatRate(peakIn);
    peakOutEl.textContent = formatRate(peakOut);
    avgInEl.textContent = formatRate(avg(inData));
    avgOutEl.textContent = formatRate(avg(outData));
    metaEl.textContent = "sfp12_wan · " + inData.length + " samples";
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
    plot = new uPlot(opts, [timestamps, inData, outData], chartEl);

    window.addEventListener("resize", function () {
      var w = chartEl.clientWidth - 32;
      if (w > 0) plot.setSize({ width: w, height: 280 });
    });
  }

  function addSample(sample) {
    timestamps.push(sample.ts);
    inData.push(sample.in_bps);
    outData.push(sample.out_bps);

    if (sample.in_bps > peakIn) peakIn = sample.in_bps;
    if (sample.out_bps > peakOut) peakOut = sample.out_bps;

    // Trim to max points
    while (timestamps.length > MAX_POINTS) {
      timestamps.shift();
      inData.shift();
      outData.shift();
    }

    if (plot) {
      plot.setData([timestamps, inData, outData]);
    }
    updateStats();
  }

  function loadHistory() {
    fetch("/history")
      .then(function (r) { return r.json(); })
      .then(function (samples) {
        if (!samples || samples.length === 0) return;
        samples.forEach(function (s) {
          timestamps.push(s.ts);
          inData.push(s.in_bps);
          outData.push(s.out_bps);
          if (s.in_bps > peakIn) peakIn = s.in_bps;
          if (s.out_bps > peakOut) peakOut = s.out_bps;
        });
        initChart();
        updateStats();
        connectSSE();
      })
      .catch(function (err) {
        console.error("history fetch failed:", err);
        initChart();
        connectSSE();
      });
  }

  function connectSSE() {
    var es = new EventSource("/stream");

    es.onopen = function () {
      statusEl.textContent = "Connected";
      statusEl.className = "status connected";
    };

    es.onmessage = function (e) {
      try {
        var sample = JSON.parse(e.data);
        addSample(sample);
      } catch (err) {
        console.error("parse error:", err);
      }
    };

    es.onerror = function () {
      statusEl.textContent = "Disconnected — reconnecting…";
      statusEl.className = "status error";
    };
  }

  loadHistory();
})();
