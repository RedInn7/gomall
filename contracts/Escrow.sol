// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/// @title Escrow
/// @notice 三方资金托管合约：买家付款 -> 合约持有资金 -> 收货或仲裁后释放给卖家/退还买家
/// @dev 状态机 Created -> Funded -> (Released | Refunded)，Disputed 是从 Funded 分叉，最终仍走 Released / Refunded
contract Escrow {
    address public immutable buyer;
    address public immutable seller;
    address public immutable arbiter;
    uint256 public immutable amount;

    enum State {
        Created,
        Funded,
        Released,
        Refunded,
        Disputed
    }

    State public state;

    /// @notice 链下唯一订单号；fund 时如果调 fundWithOrderID 会写入，普通 fund() 留空
    bytes32 public orderID;

    event Funded(uint256 amount);
    event Released(address indexed to);
    event Refunded(address indexed to);
    event Disputed(address indexed by);
    event PaymentConfirmed(bytes32 indexed orderID, address indexed buyer, uint256 amount);

    error InvalidState(State expected, State actual);
    error NotAuthorized(address caller);
    error WrongAmount(uint256 expected, uint256 actual);
    error ZeroAddress();

    modifier onlyBuyer() {
        if (msg.sender != buyer) revert NotAuthorized(msg.sender);
        _;
    }

    modifier onlySeller() {
        if (msg.sender != seller) revert NotAuthorized(msg.sender);
        _;
    }

    modifier onlyArbiter() {
        if (msg.sender != arbiter) revert NotAuthorized(msg.sender);
        _;
    }

    modifier inState(State expected) {
        if (state != expected) revert InvalidState(expected, state);
        _;
    }

    constructor(address _buyer, address _seller, address _arbiter, uint256 _amount) {
        if (_buyer == address(0) || _seller == address(0) || _arbiter == address(0)) revert ZeroAddress();
        require(_amount > 0, "amount=0");
        buyer = _buyer;
        seller = _seller;
        arbiter = _arbiter;
        amount = _amount;
        state = State.Created;
    }

    /// @notice 买家付款，不绑定 orderID
    function fund() external payable onlyBuyer inState(State.Created) {
        if (msg.value != amount) revert WrongAmount(amount, msg.value);
        state = State.Funded;
        emit Funded(msg.value);
    }

    /// @notice 买家付款并绑定链下订单号；后端 listener 订阅 PaymentConfirmed 推进订单状态
    /// @param _orderID 链下系统的订单唯一标识
    function fundWithOrderID(bytes32 _orderID) external payable onlyBuyer inState(State.Created) {
        if (msg.value != amount) revert WrongAmount(amount, msg.value);
        orderID = _orderID;
        state = State.Funded;
        emit Funded(msg.value);
        emit PaymentConfirmed(_orderID, msg.sender, msg.value);
    }

    /// @notice 买家或仲裁人确认收货，将资金释放给卖家
    function release() external {
        if (msg.sender != buyer && msg.sender != arbiter) revert NotAuthorized(msg.sender);
        if (state != State.Funded && state != State.Disputed) revert InvalidState(State.Funded, state);
        state = State.Released;
        emit Released(seller);
        _safeSend(seller, amount);
    }

    /// @notice 卖家放弃交易或仲裁人裁定退款，将资金退回买家
    function refund() external {
        if (msg.sender != seller && msg.sender != arbiter) revert NotAuthorized(msg.sender);
        if (state != State.Funded && state != State.Disputed) revert InvalidState(State.Funded, state);
        state = State.Refunded;
        emit Refunded(buyer);
        _safeSend(buyer, amount);
    }

    /// @notice 买家或卖家发起争议，冻结资金待仲裁
    function dispute() external inState(State.Funded) {
        if (msg.sender != buyer && msg.sender != seller) revert NotAuthorized(msg.sender);
        state = State.Disputed;
        emit Disputed(msg.sender);
    }

    function _safeSend(address to, uint256 value) private {
        (bool ok, ) = payable(to).call{value: value}("");
        require(ok, "transfer failed");
    }
}
