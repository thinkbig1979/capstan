#!/usr/bin/env node

/**
 * Direct Playwright E2E Test Script
 * Tests Capstan application using Playwright directly (not agent-browser)
 */

const { chromium } = require('playwright');

// Configuration
const BASE_URL = 'http://localhost:3001';
const SCREENSHOT_DIR = process.env.SCREENSHOT_DIR || './screenshots';

// Helper function to delay
const sleep = (ms) => new Promise(resolve => setTimeout(resolve, ms));

// Helper function to take screenshots
async function takeScreenshot(page, name) {
  const timestamp = new Date().toISOString().replace(/[:.]/g, '-');
  const path = `${SCREENSHOT_DIR}/${name}-${timestamp}.png`;
  await page.screenshot({ path, fullPage: true });
  console.log(`📸 Screenshot saved: ${path}`);
  return path;
}

// Test 1: Basic Navigation
async function testBasicNavigation() {
  console.log('Testing: Basic Navigation');
  
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext();
  
  try {
    const page = context.pages()[0];
    
    console.log(`1. Opening ${BASE_URL}...`);
    await page.goto(BASE_URL, { timeout: 30000 });
    await sleep(3000); // Wait for React to hydrate
    
    const title = await page.title();
    console.log(`Page title: ${title}`);
    
    const url = page.url();
    console.log(`Current URL: ${url}`);
    
    const content = await page.content();
    console.log(`Page has ${content.length} characters`);
    
    if (content.length < 100) {
      console.log('⚠ Page not fully rendered - only ' + content.length + ' characters');
      await takeScreenshot(page, 'navigation-1-not-rendered');
    } else {
      console.log('✓ Page loaded successfully');
      await takeScreenshot(page, 'navigation-1-success');
    }
    
  } catch (error) {
    console.error('✗ Navigation test failed:', error.message);
    await takeScreenshot(page, 'navigation-1-error');
  } finally {
    await context.close();
    await browser.close();
  }
}

// Test 2: Login Page Elements
async function testLoginPageElements() {
  console.log('\nTesting: Login Page Elements');
  
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext();
  try {
    const page = context.pages()[0];
    
    console.log(`1. Opening ${BASE_URL}/login...`);
    await page.goto(`${BASE_URL}/login`, { timeout: 30000 });
    await sleep(5000); // Wait for form to render
    
    console.log('2. Looking for login form elements...');
    
    // Try multiple selector strategies
    let emailInput = null;
    let passwordInput = null;
    let submitButton = null;
    
    // Try by label first
    try {
      emailInput = await page.locator('input').filter({ hasText: 'Email' }).first();
      if (await emailInput.count() > 0) {
        console.log('Found email input by label: 'Email');
      const emailPlaceholder = await emailInput.getAttribute('placeholder') || 'Email';
        console.log(`  Email input placeholder: "${emailPlaceholder}"`);
      }
    } catch (e) {}
    
    try {
      passwordInput = await page.locator('input').filter({ hasText: 'Password' }).first();
      if (await passwordInput.count() > 0) {
        console.log('Found password input by label: 'Password');
        const passwordPlaceholder = await passwordInput.getAttribute('placeholder') || 'Password';
        console.log(`  Password input placeholder: "${passwordPlaceholder}"`);
      }
    } catch (e) {}
    
    try {
      submitButton = await page.getByRole('button', { name: /Login/i });
      console.log(`  Found login button by role and text: "${await submitButton.textContent()}"`);
    } catch (e) {}
    
    // Log findings
    console.log(`Email input: ${emailInput ? '✓' : '✗'}`);
    console.log(`Password input: ${passwordInput ? '✓' : '✗'}`);
    console.log(`Login button: ${submitButton ? '✓' : '✗'}`);
    
    if (emailInput && passwordInput && submitButton) {
      console.log('✓ All login elements found');
      await takeScreenshot(page, 'login-elements-success');
    } else {
      console.log('⚠ Some login elements not found');
      await takeScreenshot(page, 'login-elements-failed');
    }
    
  } catch (error) {
    console.error('✗ Login elements test failed:', error.message);
    await takeScreenshot(page, 'login-elements-error');
  } finally {
    await context.close();
    await browser.close();
  }
}

// Test 3: Dashboard Page
async function testDashboard() {
  console.log('\nTesting: Dashboard Page');
  
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext();
  try {
    const page = context.pages()[0];
    
    console.log(`1. Opening ${BASE_URL}/dashboard...`);
    await page.goto(`${BASE_URL}/dashboard`, { timeout: 30000 });
    await sleep(3000);
    
    const title = await page.title();
    console.log(`Page title: ${title}`);
    
    const url = page.url();
    console.log(`Current URL: ${url}`);
    
    // Check for stack-related content
    const content = await page.content();
    const contentLower = content.toLowerCase();
    const hasStacks = contentLower.includes('stack') || 
                     contentLower.includes('container') || 
                     contentLower.includes('service');
    
    console.log(`Page has ${content.length} characters`);
    console.log(`Has stack-related keywords: ${hasStacks ? '✓' : '✗'}`);
    
    if (content.length < 1000) {
      console.log('⚠ Dashboard not fully rendered - only ' + content.length + ' characters');
      await takeScreenshot(page, 'dashboard-not-rendered');
    } else {
      console.log('✓ Dashboard loaded successfully');
      await takeScreenshot(page, 'dashboard-success');
    }
    
  } catch (error) {
    console.error('✗ Dashboard test failed:', error.message);
    await takeScreenshot(page, 'dashboard-error');
  } finally {
    await context.close();
    await browser.close();
  }
}

// Test 4: Console Errors
async function testConsoleErrors() {
  console.log('\nTesting: Console Errors');
  
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext();
  try {
    const page = context.pages()[0];
    
    console.log(`1. Opening ${BASE_URL}...`);
    await page.goto(BASE_URL, { timeout: 30000 });
    await sleep(2000);
    
    // Collect console messages
    const messages = [];
    page.on('console', msg => {
      messages.push(msg);
      if (msg.type() === 'error') {
        console.error('  Browser error:', msg.text());
      }
    });
    
    await sleep(3000);
    
    console.log(`Collected ${messages.length} console messages`);
    
    const hasErrors = messages.some(msg => msg.type() === 'error');
    console.log(`Has errors: ${hasErrors ? '✓' : '✗'}`);
    
    if (messages.length > 0) {
      const errorMsgs = messages.filter(msg => msg.type() === 'error');
      console.log(`Found ${errorMsgs.length} error messages`);
    }
    
    // Screenshot console errors
    await takeScreenshot(page, 'console-errors');
    
  } catch (error) {
    console.error('✗ Console errors test failed:', error.message);
    await takeScreenshot(page, 'console-errors-error');
  } finally {
    page.off('console');
    await context.close();
    await browser.close();
  }
}

// Test 5: Auth Disabled Bypass
async function testAuthDisabledBypass() {
  console.log('\nTesting: Auth Disabled Bypass');
  
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext();
  try {
    console.log(`1. Opening ${BASE_URL}...`);
    await page.goto(BASE_URL, { timeout: 30000 });
    await sleep(2000);
    
    // Inject auth bypass token
    console.log('2. Injecting fake auth token...');
    await page.addInitScript({ content: `
      console.log('Injecting auth bypass token...');
      localStorage.setItem('token', 'fake-test-token-${Date.now()}');
      console.log('Token set, reloading...');
      location.reload();
    ` });
    
    await sleep(3000);
    
    // Check if we see dashboard
    const url = page.url();
    console.log(`URL after injection: ${url}`);
    
    const content = await page.content();
    console.log(`Page has ${content.length} characters`);
    
    if (content.length > 1000) {
      console.log('✓ Auth bypass successful - dashboard rendered');
      await takeScreenshot(page, 'auth-bypass-success');
    } else {
      console.log('⚠ Auth bypass failed - page not rendered');
      await takeScreenshot(page, 'auth-bypass-failed');
    }
    
  } catch (error) {
    console.error('✗ Auth bypass test failed:', error.message);
    await takeScreenshot(page, 'auth-bypass-error');
  } finally {
    await context.close();
    await browser.close();
  }
}

// Run all tests
async function runAllTests() {
  console.log('='.repeat(60));
  console.log('DOCKER MANAGER E2E TESTS - DIRECT PLAYWRIGHT');
  console.log('='.repeat(60));
  console.log(`Base URL: ${BASE_URL}`);
  console.log(`Screenshot Directory: ${SCREENSHOT_DIR}`);
  console.log('='.repeat(60));
  
  console.log('');
  console.log('Testing Strategy:');
  console.log('  1. Test basic navigation to see if app loads');
  console.log('2. Try to find login elements with multiple selector strategies');
  console.log(' 3. Test dashboard to see if stacks appear');
  console.log('4. Check console for errors');
  console.log(' 5. Test auth bypass to skip login');
  console.log('');
  console.log('='.repeat(60));
  console.log('Running all tests...');
  console.log('='.repeat(60));
  
  await testBasicNavigation();
  await sleep(1000);
  await testLoginPageElements();
  await sleep(1000);
  await testDashboard();
  await sleep(1000);
  await testConsoleErrors();
  await testAuthDisabledBypass();
  await sleep(1000);
  
  console.log('');
  console.log('='.repeat(60));
  console.log('TESTS COMPLETE');
  console.log(`='.repeat(60));
  console.log(`Results saved to: ${SCREENSHOT_DIR}/`);
  console.log('='.repeat(60));
}

// Main execution
if (require.main === module) {
  runAllTests().catch(error => {
    console.error('Fatal error:', error);
    process.exit(1);
  });
} else {
  module.exports = {
    runAllTests,
    testBasicNavigation,
    testLoginPageElements,
    testDashboard,
    testConsoleErrors,
    testAuthDisabledBypass,
  };
}
