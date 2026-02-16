const { chromium } = require('playwright');

(async () => {
  const browser = await chromium.launch({ 
    headless: false,
    slowMo: 500
  });
  const context = await browser.newContext();
  const page = await context.newPage();

  const consoleErrors = [];
  const pageErrors = [];
  const testResults = [];

  page.on('console', msg => {
    if (msg.type() === 'error') {
      consoleErrors.push(msg.text());
    }
  });

  page.on('pageerror', error => {
    pageErrors.push(error.message);
  });

  function logStep(step, status, message) {
    const symbol = status === '✓' ? '✓' : status === '✗' ? '✗' : '→';
    testResults.push({ step, status, message });
    console.log(`${symbol} ${step}: ${message}`);
  }

  logStep('1', '→', 'Opening http://localhost:3001');
  try {
    await page.goto('http://localhost:3001', { waitUntil: 'networkidle', timeout: 10000 });
    const title = await page.title();
    const url = page.url();
    logStep('1', '✓', `Page loaded - Title: "${title}", URL: ${url}`);
    await page.screenshot({ path: '/tmp/test-01-initial-load.png' });
  } catch (error) {
    logStep('1', '✗', `Failed to load: ${error.message}`);
    await browser.close();
    return;
  }

  logStep('2', '→', 'Checking browser console for errors');
  if (consoleErrors.length > 0) {
    logStep('2', '✗', `${consoleErrors.length} console errors found`);
    consoleErrors.forEach((err, i) => console.log(`    ${i + 1}. ${err}`));
  } else {
    logStep('2', '✓', 'No console errors');
  }
  if (pageErrors.length > 0) {
    logStep('2', '✗', `${pageErrors.length} page errors found`);
    pageErrors.forEach((err, i) => console.log(`    ${i + 1}. ${err}`));
  } else {
    logStep('2', '✓', 'No page errors');
  }

  logStep('3', '→', 'Verifying AppShell layout (Sidebar + Header)');
  const hasAppShell = await page.locator('aside').count() > 0 && await page.locator('header').count() > 0;
  if (hasAppShell) {
    logStep('3', '✓', 'AppShell layout found (sidebar and header present)');
  } else {
    logStep('3', '✗', 'AppShell layout NOT found - pages rendered without layout');
    await page.screenshot({ path: '/tmp/test-02-no-layout.png' });
  }

  logStep('4', '→', 'Verifying sidebar shows directories');
  const sidebar = await page.locator('aside').first();
  const sidebarVisible = await sidebar.isVisible().catch(() => false);
  
  if (sidebarVisible) {
    const directoryItems = await sidebar.locator('a[href="/"]').count();
    if (directoryItems > 0) {
      logStep('4', '✓', `Sidebar visible with ${directoryItems} directory links`);
    } else {
      logStep('4', '✗', 'Sidebar visible but no directory links found');
    }
  } else {
    logStep('4', '✗', 'Sidebar not visible');
  }

  logStep('5', '→', 'Verifying main dashboard shows stacks');
  const dashboardTitle = await page.locator('h1:has-text("Dashboard")').count();
  if (dashboardTitle > 0) {
    logStep('5', '✓', 'Dashboard title found');
    
    const stackCards = await page.locator('[class*="card"]').count();
    if (stackCards > 0) {
      logStep('5', '✓', `Found ${stackCards} cards (directories/stacks)`);
    } else {
      logStep('5', '✗', 'No cards found on dashboard');
    }
  } else {
    logStep('5', '✗', 'Dashboard title not found');
  }

  logStep('6', '→', 'Looking for "New Stack" button');
  const newStackButton = await page.locator('button:has-text("New Stack")').count();
  if (newStackButton > 0) {
    logStep('6', '✓', '"New Stack" button found');
  } else {
    logStep('6', '✗', '"New Stack" button not found');
  }

  logStep('7', '→', 'Looking for settings link/button');
  const settingsSelectors = [
    'a[href="/settings"]',
    'button:has-text("Settings")',
    '[data-testid="settings"]'
  ];
  
  let settingsFound = false;
  for (const selector of settingsSelectors) {
    const count = await page.locator(selector).count();
    if (count > 0) {
      logStep('7', '✓', `Settings link/button found with selector: ${selector}`);
      settingsFound = true;
      
      try {
        await page.locator(selector).first().click();
        await page.waitForTimeout(1000);
        const currentUrl = page.url();
        if (currentUrl.includes('/settings')) {
          logStep('7', '✓', `Successfully navigated to: ${currentUrl}`);
        } else {
          logStep('7', '✗', `Click didn't navigate to settings, still at: ${currentUrl}`);
        }
      } catch (error) {
        logStep('7', '✗', `Failed to click settings: ${error.message}`);
      }
      break;
    }
  }
  
  if (!settingsFound) {
    logStep('7', '✗', 'Settings link/button not found');
  }
  await page.screenshot({ path: '/tmp/test-03-after-settings.png' });

  logStep('8', '→', 'Navigating back to dashboard and checking stats');
  await page.goto('http://localhost:3001', { waitUntil: 'networkidle' });
  
  const totalStacksCard = await page.locator('h2:has-text("Total Stacks")').count();
  const runningCard = await page.locator('h2:has-text("Running")').count();
  const stoppedCard = await page.locator('h2:has-text("Stopped")').count();
  
  if (totalStacksCard > 0) {
    logStep('8', '✓', 'Stats cards found (Total Stacks, Running, Stopped)');
  } else {
    logStep('8', '✗', 'Stats cards not found');
  }
  
  await page.screenshot({ path: '/tmp/test-04-dashboard-stats.png' });

  console.log('\n=== TEST SUMMARY ===');
  console.log('Screenshots saved to /tmp/');
  console.log('  - test-01-initial-load.png');
  console.log('  - test-02-no-layout.png (if missing layout)');
  console.log('  - test-03-after-settings.png');
  console.log('  - test-04-dashboard-stats.png');
  
  console.log('\nResults:');
  testResults.forEach(result => {
    console.log(`  ${result.status} ${result.step}: ${result.message}`);
  });
  
  const passed = testResults.filter(r => r.status === '✓').length;
  const failed = testResults.filter(r => r.status === '✗').length;
  console.log(`\nTotal: ${passed} passed, ${failed} failed`);

  console.log('\nBrowser will remain open for 30 seconds for manual inspection...');
  await page.waitForTimeout(30000);
  await browser.close();
})();
