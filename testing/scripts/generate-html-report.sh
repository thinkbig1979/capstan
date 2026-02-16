#!/bin/bash

# Generate HTML Test Report
# Creates a visual HTML report from JSON test results

set -euo pipefail

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TESTING_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"
REPORTS_DIR="$TESTING_DIR/reports"
HTML_DIR="$REPORTS_DIR/html"

# Colors for console output
export GREEN='\033[0;32m'
export BLUE='\033[0;34m'
export NC='\033[0m'

log_info() {
  echo -e "${BLUE}[INFO]${NC} $*"
}

log_success() {
  echo -e "${GREEN}[SUCCESS]${NC} $*"
}

# Create HTML report
create_html_report() {
  local results_file="$REPORTS_DIR/results.json"
  
  if [ ! -f "$results_file" ]; then
    log_info "No test results found, generating template report..."
    results_file=""
  fi
  
  log_info "Generating HTML report..."
  mkdir -p "$HTML_DIR"
  
  local report_file="$HTML_DIR/report.html"
  
  # Generate HTML
  cat > "$report_file" << 'EOF'
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Docker Manager E2E Test Report</title>
  <style>
    * {
      margin: 0;
      padding: 0;
      box-sizing: border-box;
    }
    
    body {
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, sans-serif;
      background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
      min-height: 100vh;
      padding: 20px;
    }
    
    .container {
      max-width: 1400px;
      margin: 0 auto;
    }
    
    .header {
      background: white;
      border-radius: 12px;
      padding: 30px;
      margin-bottom: 20px;
      box-shadow: 0 4px 6px rgba(0, 0, 0, 0.1);
    }
    
    .header h1 {
      color: #667eea;
      font-size: 28px;
      margin-bottom: 10px;
    }
    
    .header .subtitle {
      color: #666;
      font-size: 14px;
    }
    
    .summary {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
      gap: 20px;
      margin-bottom: 20px;
    }
    
    .summary-card {
      background: white;
      border-radius: 12px;
      padding: 25px;
      box-shadow: 0 4px 6px rgba(0, 0, 0, 0.1);
      text-align: center;
    }
    
    .summary-card h3 {
      font-size: 14px;
      color: #666;
      margin-bottom: 10px;
      text-transform: uppercase;
      letter-spacing: 1px;
    }
    
    .summary-card .value {
      font-size: 36px;
      font-weight: bold;
      color: #333;
    }
    
    .summary-card.passed .value { color: #10b981; }
    .summary-card.failed .value { color: #ef4444; }
    .summary-card.skipped .value { color: #f59e0b; }
    .summary-card.rate .value { color: #667eea; }
    
    .progress-bar {
      background: #e5e7eb;
      border-radius: 10px;
      height: 10px;
      overflow: hidden;
      margin-top: 10px;
    }
    
    .progress-fill {
      height: 100%;
      background: linear-gradient(90deg, #10b981 0%, #059669 100%);
      transition: width 0.5s ease;
    }
    
    .domains {
      background: white;
      border-radius: 12px;
      padding: 30px;
      margin-bottom: 20px;
      box-shadow: 0 4px 6px rgba(0, 0, 0, 0.1);
    }
    
    .domains h2 {
      color: #333;
      margin-bottom: 20px;
      font-size: 20px;
    }
    
    .domain-card {
      border: 2px solid #e5e7eb;
      border-radius: 8px;
      padding: 20px;
      margin-bottom: 15px;
      transition: all 0.3s ease;
    }
    
    .domain-card.passed {
      border-color: #10b981;
      background: #f0fdf4;
    }
    
    .domain-card.failed {
      border-color: #ef4444;
      background: #fef2f2;
    }
    
    .domain-card h3 {
      color: #333;
      margin-bottom: 10px;
      font-size: 16px;
    }
    
    .domain-stats {
      display: flex;
      gap: 20px;
      font-size: 14px;
      color: #666;
    }
    
    .domain-stats span {
      padding: 4px 12px;
      border-radius: 20px;
      background: white;
      font-weight: 500;
    }
    
    .domain-stats .passed { color: #10b981; }
    .domain-stats .failed { color: #ef4444; }
    
    .failures {
      background: white;
      border-radius: 12px;
      padding: 30px;
      box-shadow: 0 4px 6px rgba(0, 0, 0, 0.1);
    }
    
    .failures h2 {
      color: #333;
      margin-bottom: 20px;
      font-size: 20px;
    }
    
    .failure-item {
      border-left: 4px solid #ef4444;
      padding: 15px 20px;
      background: #fef2f2;
      margin-bottom: 15px;
      border-radius: 4px;
    }
    
    .failure-item h3 {
      color: #ef4444;
      margin-bottom: 8px;
      font-size: 14px;
    }
    
    .failure-item .error {
      color: #666;
      font-size: 13px;
      line-height: 1.5;
    }
    
    .screenshot-link {
      display: inline-block;
      margin-top: 10px;
      padding: 6px 12px;
      background: #667eea;
      color: white;
      text-decoration: none;
      border-radius: 4px;
      font-size: 12px;
    }
    
    .footer {
      text-align: center;
      padding: 30px;
      color: white;
      font-size: 14px;
    }
    
    .timestamp {
      margin-top: 10px;
      font-size: 12px;
      opacity: 0.8;
    }
    
    @media (max-width: 768px) {
      .summary {
        grid-template-columns: 1fr;
      }
    }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>🐳 Docker Manager E2E Test Report</h1>
      <div class="subtitle">End-to-End Browser Testing Results</div>
      <div class="timestamp" id="timestamp"></div>
    </div>
    
    <div class="summary">
      <div class="summary-card">
        <h3>Total Tests</h3>
        <div class="value" id="total-tests">0</div>
      </div>
      <div class="summary-card passed">
        <h3>Passed</h3>
        <div class="value" id="passed-tests">0</div>
      </div>
      <div class="summary-card failed">
        <h3>Failed</h3>
        <div class="value" id="failed-tests">0</div>
      </div>
      <div class="summary-card skipped">
        <h3>Skipped</h3>
        <div class="value" id="skipped-tests">0</div>
      </div>
      <div class="summary-card rate">
        <h3>Success Rate</h3>
        <div class="value" id="success-rate">0%</div>
        <div class="progress-bar">
          <div class="progress-fill" id="progress-fill"></div>
        </div>
      </div>
    </div>
    
    <div class="domains">
      <h2>📊 Domain Results</h2>
      <div id="domains-list"></div>
    </div>
    
    <div class="failures" id="failures-section">
      <h2>❌ Failures</h2>
      <div id="failures-list"></div>
    </div>
  </div>
  
  <div class="footer">
    <p>Generated by Docker Manager Test Orchestrator</p>
  </div>

  <script>
EOF

  # Add JavaScript to populate data
  if [ -f "$results_file" ]; then
    # Read results and generate JavaScript
    cat >> "$report_file" << EOF
    // Load test results
    const results = $(cat "$results_file");
    
    // Update summary
    document.getElementById('total-tests').textContent = results.test_run.total_tests;
    document.getElementById('passed-tests').textContent = results.test_run.passed;
    document.getElementById('failed-tests').textContent = results.test_run.failed;
    document.getElementById('skipped-tests').textContent = results.test_run.skipped;
    document.getElementById('success-rate').textContent = results.test_run.success_rate + '%';
    document.getElementById('progress-fill').style.width = results.test_run.success_rate + '%';
    document.getElementById('timestamp').textContent = 'Generated: ' + new Date(results.test_run.timestamp).toLocaleString();
    
    // Render domains
    const domainsList = document.getElementById('domains-list');
    if (results.domains) {
      results.domains.forEach(domain => {
        const statusClass = domain.failed === 0 ? 'passed' : 'failed';
        const card = document.createElement('div');
        card.className = 'domain-card ' + statusClass;
        card.innerHTML = \`
          <h3>\${domain.name}</h3>
          <div class="domain-stats">
            <span class="passed">Passed: \${domain.passed}</span>
            <span class="failed">Failed: \${domain.failed}</span>
          </div>
        \`;
        domainsList.appendChild(card);
      });
    } else {
      domainsList.innerHTML = '<p style="color: #666;">No domain results available</p>';
    }
    
    // Render failures
    const failuresList = document.getElementById('failures-list');
    const failuresSection = document.getElementById('failures-section');
    if (results.failures && results.failures.length > 0) {
      results.failures.forEach(failure => {
        const item = document.createElement('div');
        item.className = 'failure-item';
        item.innerHTML = \`
          <h3>\${failure.test_id}: \${failure.test_name}</h3>
          <div class="error">\${failure.error}</div>
          \${failure.screenshot ? '<a href="../screenshots/' + failure.screenshot + '" target="_blank" class="screenshot-link">View Screenshot</a>' : ''}
        \`;
        failuresList.appendChild(item);
      });
    } else {
      failuresSection.style.display = 'none';
    }
EOF
  else
    # No results, show empty state
    cat >> "$report_file" << 'EOF'
    // No test results available
    document.getElementById('total-tests').textContent = '0';
    document.getElementById('passed-tests').textContent = '0';
    document.getElementById('failed-tests').textContent = '0';
    document.getElementById('skipped-tests').textContent = '0';
    document.getElementById('success-rate').textContent = '0%';
    document.getElementById('timestamp').textContent = 'Generated: ' + new Date().toLocaleString();
    
    document.getElementById('domains-list').innerHTML = '<p style="color: #666;">No test results available yet. Run the test suite first.</p>';
    document.getElementById('failures-section').style.display = 'none';
EOF
  fi

  # Close script and HTML
  cat >> "$report_file" << 'EOF'
  </script>
</body>
</html>
EOF

  log_success "HTML report generated: $report_file"
}

# Main execution
main() {
  log_info "Generating HTML test report..."
  
  create_html_report
  
  log_info "Open report in browser:"
  echo "  file://$HTML_DIR/report.html"
  echo ""
  log_info "Or copy to a web server to view."
}

# Run main
main "$@"
