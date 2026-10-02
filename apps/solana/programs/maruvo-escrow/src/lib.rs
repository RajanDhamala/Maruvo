use anchor_lang::prelude::*;
use anchor_lang::system_program::{self, Transfer};

declare_id!("FEV9eR2WC2pPo6RDWEx9iSwvBHfokVpBED246fdUg2hd");

#[program]
pub mod maruvo_escrow {
    use super::*;

    pub fn fund(
        ctx: Context<Fund>,
        post_id: u64,
        amount: u64,
        worker: Pubkey,
        reviewer: Pubkey,
        agreement_hash: [u8; 32],
    ) -> Result<()> {
        require!(
            worker != ctx.accounts.poster.key() && worker != Pubkey::default(),
            EscrowError::InvalidWorker
        );
        require!(reviewer != Pubkey::default(), EscrowError::InvalidReviewer);
        require!(amount <= i64::MAX as u64, EscrowError::InvalidAmount);
        let escrow = &mut ctx.accounts.escrow;
        escrow.post_id = post_id;
        escrow.poster = ctx.accounts.poster.key();
        escrow.worker = worker;
        escrow.reviewer = reviewer;
        escrow.amount = amount;
        escrow.agreement_hash = agreement_hash;
        escrow.state = 0;
        escrow.bump = ctx.bumps.escrow;
        system_program::transfer(
            CpiContext::new(
                ctx.accounts.system_program.to_account_info(),
                Transfer {
                    from: ctx.accounts.poster.to_account_info(),
                    to: escrow.to_account_info(),
                },
            ),
            amount,
        )?;
        Ok(())
    }

    pub fn release(ctx: Context<Settle>) -> Result<()> {
        let recipient = ctx.accounts.worker.to_account_info();
        settle(ctx, recipient, 1)
    }

    pub fn refund(ctx: Context<Settle>) -> Result<()> {
        let recipient = ctx.accounts.poster.to_account_info();
        settle(ctx, recipient, 2)
    }
}

fn settle(ctx: Context<Settle>, recipient: AccountInfo, state: u8) -> Result<()> {
    require!(ctx.accounts.escrow.state == 0, EscrowError::AlreadySettled);
    let escrow = ctx.accounts.escrow.to_account_info();
    let amount = ctx.accounts.escrow.amount;
    let remaining = escrow
        .lamports()
        .checked_sub(amount)
        .ok_or(EscrowError::InvalidAmount)?;
    require!(
        remaining >= Rent::get()?.minimum_balance(Escrow::SPACE),
        EscrowError::InvalidAmount
    );
    let recipient_balance = recipient
        .lamports()
        .checked_add(amount)
        .ok_or(EscrowError::InvalidAmount)?;
    **escrow.try_borrow_mut_lamports()? = remaining;
    **recipient.try_borrow_mut_lamports()? = recipient_balance;
    // Keep the receipt account so the same post cannot be funded twice.
    ctx.accounts.escrow.state = state;
    Ok(())
}

#[derive(Accounts)]
#[instruction(post_id: u64)]
pub struct Fund<'info> {
    #[account(mut)]
    pub poster: Signer<'info>,
    #[account(init, payer = poster, space = Escrow::SPACE,
        seeds = [b"escrow", poster.key().as_ref(), &post_id.to_le_bytes()], bump)]
    pub escrow: Account<'info, Escrow>,
    pub system_program: Program<'info, System>,
}

#[derive(Accounts)]
pub struct Settle<'info> {
    pub reviewer: Signer<'info>,
    #[account(mut, seeds = [b"escrow", escrow.poster.as_ref(), &escrow.post_id.to_le_bytes()], bump = escrow.bump,
        has_one = reviewer, has_one = poster, has_one = worker)]
    pub escrow: Account<'info, Escrow>,
    /// CHECK: Destination must match the poster saved in escrow.
    #[account(mut)]
    pub poster: UncheckedAccount<'info>,
    /// CHECK: Destination must match the worker saved in escrow.
    #[account(mut)]
    pub worker: UncheckedAccount<'info>,
}

#[account]
pub struct Escrow {
    pub post_id: u64,
    pub poster: Pubkey,
    pub worker: Pubkey,
    pub reviewer: Pubkey,
    pub amount: u64,
    pub agreement_hash: [u8; 32],
    pub state: u8,
    pub bump: u8,
}

impl Escrow {
    pub const SPACE: usize = 8 + 8 + 32 * 3 + 8 + 32 + 2;
}

#[error_code]
pub enum EscrowError {
    #[msg("Worker must be a different, valid wallet")]
    InvalidWorker,
    #[msg("Reviewer must be a valid wallet")]
    InvalidReviewer,
    #[msg("Invalid escrow amount or balance")]
    InvalidAmount,
    #[msg("Escrow has already been settled")]
    AlreadySettled,
}
