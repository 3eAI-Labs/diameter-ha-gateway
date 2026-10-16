export const COMPANY_INFO = {
  name: '3eAI Labs',
  // The registered entity, confirmed against the Stripe merchant account
  // ("3EAI LABS LTD") and the doc-tools branded footer. This previously held a
  // placeholder Turkish company name marked as a sample; it reached a product
  // footer before the mismatch was caught, and a wrong legal name is not
  // cosmetic where invoices and payment are involved.
  legalName: '3eAI Labs Ltd',
  website: 'https://3eai-labs.com',
  emails: {
    info: 'info@3eai-labs.com',
    support: 'support@3eai-labs.com',
    sales: 'sales@3eai-labs.com',
    licensing: 'licensing@3eai-labs.com'
  },
  social: {
    linkedin: 'https://linkedin.com/company/3eai-labs',
    twitter: 'https://twitter.com/3eai_labs',
    github: 'https://github.com/3eai-labs'
  }
}
