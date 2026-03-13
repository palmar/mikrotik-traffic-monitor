(function () {
  "use strict";

  var MAX_POINTS = 240;
  var STORAGE_KEY = "mtm-selected";

  // State
  var devices = [];       // [{name, interfaces: [str]}]
  var data = {};          // "device/iface" -> {timestamps, inData, outData, peakIn, peakOut}
  var panels = {};        // "device/iface" -> {el, chart, statsEls}
  var selected = {};      // "device/iface" -> bool

  var statusEl = document.getElementById("status");
  var selectorEl = document.getElementById("selector");
  var panelsEl = document.getElementById("panels");

  function bufKey(device, iface) {
    return device + "/" + iface;
  }

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

  function ensureData(key) {
    if (!data[key]) {
      data[key] = { timestamps: [], inData: [], outData: [], peakIn: 0, peakOut: 0 };
    }
    return data[key];
  }

  // Persistence
  function loadSelection() {
    try {
      var saved = JSON.parse(localStorage.getItem(STORAGE_KEY));
      if (saved && typeof saved === "object") return saved;
    } catch (e) {}
    return null;
  }

  function saveSelection() {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(selected));
  }

  function rediscoverDevice(deviceName, btn) {
    btn.disabled = true;
    btn.textContent = "Discovering\u2026";

    fetch("/api/devices/rediscover", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ device: deviceName })
    })
      .then(function (r) {
        if (!r.ok) throw new Error("rediscovery failed");
        return r.json();
      })
      .then(function (updatedDev) {
        // Update device in our list
        for (var i = 0; i < devices.length; i++) {
          if (devices[i].name === updatedDev.name) {
            devices[i].interfaces = updatedDev.interfaces || [];
            break;
          }
        }
        renderSelector();
        syncPanels();
      })
      .catch(function (err) {
        console.error("rediscover failed:", err);
        btn.disabled = false;
        btn.textContent = "Rediscover";
      });
  }

  // Selector UI
  function renderSelector() {
    selectorEl.innerHTML = "";
    var saved = loadSelection();

    devices.forEach(function (dev) {
      var group = document.createElement("div");
      group.className = "device-selector";

      var labelRow = document.createElement("div");
      labelRow.className = "device-selector-header";

      var label = document.createElement("div");
      label.className = "device-selector-label";
      label.textContent = dev.name;
      labelRow.appendChild(label);

      var rediscoverBtn = document.createElement("button");
      rediscoverBtn.className = "rediscover-btn";
      rediscoverBtn.textContent = "Rediscover";
      rediscoverBtn.addEventListener("click", (function (name, btn) {
        return function () { rediscoverDevice(name, btn); };
      })(dev.name, rediscoverBtn));
      labelRow.appendChild(rediscoverBtn);

      group.appendChild(labelRow);

      var chips = document.createElement("div");
      chips.className = "iface-chips";

      dev.interfaces.forEach(function (iface) {
        var key = bufKey(dev.name, iface);

        // Default selection: use saved, or select first 2 interfaces per device
        if (saved && saved.hasOwnProperty(key)) {
          selected[key] = saved[key];
        } else if (!saved) {
          selected[key] = dev.interfaces.indexOf(iface) < 2;
        }

        var chip = document.createElement("button");
        chip.className = "iface-chip" + (selected[key] ? " selected" : "");
        chip.textContent = iface;
        chip.addEventListener("click", function () {
          selected[key] = !selected[key];
          chip.className = "iface-chip" + (selected[key] ? " selected" : "");
          saveSelection();
          syncPanels();
        });
        chips.appendChild(chip);
      });

      group.appendChild(chips);
      selectorEl.appendChild(group);
    });
  }

  // Panel management
  function createPanel(device, iface) {
    var key = bufKey(device, iface);
    var el = document.createElement("div");
    el.className = "iface-panel";

    var header = document.createElement("div");
    header.className = "panel-header";

    var left = document.createElement("div");
    var title = document.createElement("span");
    title.className = "panel-title";
    title.textContent = iface;
    left.appendChild(title);
    if (devices.length > 1) {
      var devLabel = document.createElement("span");
      devLabel.className = "panel-device";
      devLabel.textContent = " \u00b7 " + device;
      left.appendChild(devLabel);
    }
    header.appendChild(left);

    var meta = document.createElement("span");
    meta.className = "panel-meta";
    header.appendChild(meta);
    el.appendChild(header);

    var stats = document.createElement("div");
    stats.className = "panel-stats";
    var statNames = [
      { label: "Inbound", id: "in" },
      { label: "Outbound", id: "out" },
      { label: "Peak In", id: "peak-in" },
      { label: "Peak Out", id: "peak-out" },
      { label: "Avg In", id: "avg-in" },
      { label: "Avg Out", id: "avg-out" }
    ];
    var statsEls = {};
    statNames.forEach(function (s) {
      var card = document.createElement("div");
      card.className = "stat-card";
      var lbl = document.createElement("span");
      lbl.className = "label";
      lbl.textContent = s.label;
      var val = document.createElement("span");
      val.className = "value";
      val.textContent = "\u2014";
      card.appendChild(lbl);
      card.appendChild(val);
      stats.appendChild(card);
      statsEls[s.id] = val;
    });
    el.appendChild(stats);

    var chartEl = document.createElement("div");
    chartEl.className = "panel-chart";
    el.appendChild(chartEl);

    var d = ensureData(key);
    var width = Math.max(panelsEl.clientWidth - 64, 200);
    var opts = {
      width: width,
      height: 200,
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
          font: "11px system-ui"
        },
        {
          stroke: "#8b8fa3",
          grid: { stroke: "rgba(42,45,58,0.6)", width: 1 },
          ticks: { stroke: "rgba(42,45,58,0.6)", width: 1 },
          font: "11px system-ui",
          values: function (u, vals) { return vals.map(formatRate); },
          size: 80
        }
      ],
      series: [
        {},
        { label: "Inbound", stroke: "#4ade80", width: 2, fill: "rgba(74,222,128,0.08)" },
        { label: "Outbound", stroke: "#60a5fa", width: 2, fill: "rgba(96,165,250,0.08)" }
      ]
    };

    var chart = new uPlot(opts, [d.timestamps, d.inData, d.outData], chartEl);

    panels[key] = { el: el, chart: chart, chartEl: chartEl, statsEls: statsEls, meta: meta };
    updatePanelStats(key);
    return el;
  }

  function updatePanelStats(key) {
    var p = panels[key];
    var d = data[key];
    if (!p || !d) return;

    var lastIn = d.inData.length > 0 ? d.inData[d.inData.length - 1] : null;
    var lastOut = d.outData.length > 0 ? d.outData[d.outData.length - 1] : null;
    p.statsEls["in"].textContent = formatRate(lastIn);
    p.statsEls["out"].textContent = formatRate(lastOut);
    p.statsEls["peak-in"].textContent = formatRate(d.peakIn);
    p.statsEls["peak-out"].textContent = formatRate(d.peakOut);
    p.statsEls["avg-in"].textContent = formatRate(avg(d.inData));
    p.statsEls["avg-out"].textContent = formatRate(avg(d.outData));
    p.meta.textContent = d.inData.length + " samples";
  }

  function syncPanels() {
    // Remove panels that are no longer selected
    Object.keys(panels).forEach(function (key) {
      if (!selected[key]) {
        if (panels[key].chart) panels[key].chart.destroy();
        if (panels[key].el.parentNode) panels[key].el.parentNode.removeChild(panels[key].el);
        delete panels[key];
      }
    });

    // Add panels that are selected but not yet created, in device order
    devices.forEach(function (dev) {
      dev.interfaces.forEach(function (iface) {
        var key = bufKey(dev.name, iface);
        if (selected[key] && !panels[key]) {
          var el = createPanel(dev.name, iface);
          panelsEl.appendChild(el);
        }
      });
    });
  }

  function addSample(device, iface, sample) {
    var key = bufKey(device, iface);
    var d = ensureData(key);
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

    var p = panels[key];
    if (p) {
      p.chart.setData([d.timestamps, d.inData, d.outData]);
      updatePanelStats(key);
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
        addSample(msg.device, msg.iface, { ts: msg.ts, in_bps: msg.in_bps, out_bps: msg.out_bps });
      } catch (err) {
        console.error("parse error:", err);
      }
    };

    es.onerror = function () {
      statusEl.textContent = "Disconnected \u2014 reconnecting\u2026";
      statusEl.className = "status error";
    };
  }

  // Handle window resize for all charts
  window.addEventListener("resize", function () {
    var width = Math.max(panelsEl.clientWidth - 64, 200);
    Object.keys(panels).forEach(function (key) {
      var p = panels[key];
      if (p && p.chart) {
        p.chart.setSize({ width: width, height: 200 });
      }
    });
  });

  function init() {
    fetch("/api/devices")
      .then(function (r) { return r.json(); })
      .then(function (devs) {
        devices = devs || [];
        renderSelector();
        return fetch("/history");
      })
      .then(function (r) { return r.json(); })
      .then(function (history) {
        // history: { "device": { "iface": [{ts, in_bps, out_bps}] } }
        Object.keys(history).forEach(function (device) {
          var devHistory = history[device];
          Object.keys(devHistory).forEach(function (iface) {
            var key = bufKey(device, iface);
            var d = ensureData(key);
            var samples = devHistory[iface] || [];
            samples.forEach(function (s) {
              d.timestamps.push(s.ts);
              d.inData.push(s.in_bps);
              d.outData.push(s.out_bps);
              if (s.in_bps > d.peakIn) d.peakIn = s.in_bps;
              if (s.out_bps > d.peakOut) d.peakOut = s.out_bps;
            });
          });
        });
        syncPanels();
        connectSSE();
      })
      .catch(function (err) {
        console.error("init failed:", err);
        connectSSE();
      });
  }

  init();
})();
